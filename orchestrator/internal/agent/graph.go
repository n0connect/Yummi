package agent

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/n0connect/yummi/orchestrator/internal/conductor"
)

// Node name constants.
const (
	nodeStep      = "step"
	nodeTool      = "tool"
	nodeConsult   = "consult"
	nodeRetry     = "retry"
	nodeSummarize = "summarize"
	nodeApproval  = "approval"
	nodeFinish    = "finish"
	nodeAsk       = "ask"
	nodeHandoff   = "handoff"
)

// Edge name constants.
const (
	edgeRunCommand       = "run_command"
	edgeFinish           = "finish"
	edgeAskOperator      = "ask_operator"
	edgeUpdatePlan       = "update_plan"
	edgeLLMError         = "llm_error"
	edgeBudgetOut        = "budget_out"
	edgeContextFull      = "context_full"
	edgeOK               = "ok"
	edgeIsError          = "is_error"
	edgeRequiresApproval = "requires_approval"
	edgeStuck            = "stuck"
	edgeRetryAgain       = "retry"
	edgeGiveUp           = "give_up"
	edgeApproved         = "approved"
	edgeDenied           = "denied"
	edgeDone             = "done"
	edgeHandoff          = "handoff"
	// Live-control edges (consult channel).
	edgeLongRunning = "long_running"
	edgeContinue    = "continue"
	edgeAbort       = "abort"
	edgeChangeGoal  = "change_goal"
	edgeStopTask    = "stop_task"
	edgeSendFull    = "send_full"
)

// NodeFunc is a state machine node function.
type NodeFunc func(ctx context.Context, s *GraphState) (nextEdge string, err error)

// graphEdges is the routing table: node → edge → nextNode.
// Terminal nodes (finish, ask) have no entries — they return edgeDone and Execute stops.
// retryNode is special: the edgeRetryAgain target is dynamic (s.retryCtx.Caller).
var graphEdges = map[string]map[string]string{
	nodeStep: {
		edgeContextFull: nodeSummarize,
		edgeBudgetOut:   nodeFinish,
		edgeRunCommand:  nodeTool,
		edgeFinish:      nodeFinish,
		edgeAskOperator: nodeAsk,
		edgeUpdatePlan:  nodeStep,
		edgeSendFull:    nodeStep,
		edgeStopTask:    nodeFinish,
		edgeHandoff:     nodeHandoff,
		edgeLLMError:    nodeRetry,
	},
	nodeHandoff: {
		edgeOK: nodeStep,
	},
	nodeTool: {
		edgeOK:               nodeStep,
		edgeIsError:          nodeStep,
		edgeRequiresApproval: nodeApproval,
		edgeStuck:            nodeConsult,
		edgeLongRunning:      nodeConsult,
	},
	nodeConsult: {
		edgeContinue:    nodeStep,
		edgeAbort:       nodeStep,
		edgeChangeGoal:  nodeStep,
		edgeStopTask:    nodeFinish,
		edgeGiveUp:      nodeAsk,
		edgeLLMError:    nodeRetry,
		edgeLongRunning: nodeConsult,
		edgeOK:          nodeStep,
		edgeIsError:     nodeStep,
	},
	nodeRetry: {
		edgeGiveUp: nodeAsk,
		// edgeRetryAgain → s.retryCtx.Caller (handled dynamically in Execute)
	},
	nodeApproval: {
		edgeApproved: nodeStep,
		edgeDenied:   nodeStep,
		edgeGiveUp:   nodeAsk,
	},
	nodeSummarize: {
		edgeDone: nodeStep,
	},
}

// nodes maps node names to their handler functions. Populated in init() rather
// than as a literal initializer: handoffNode transitively calls Execute (which
// reads `nodes`), so a static initializer would form an initialization cycle.
// init() bodies are exempt from that analysis.
var nodes map[string]NodeFunc

func init() {
	nodes = map[string]NodeFunc{
		nodeStep:      stepNode,
		nodeTool:      toolNode,
		nodeConsult:   consultNode,
		nodeRetry:     retryNode,
		nodeSummarize: summarizeNode,
		nodeApproval:  approvalNode,
		nodeFinish:    finishNode,
		nodeAsk:       askNode,
		nodeHandoff:   handoffNode,
	}
}

// Stuck detection constants.
const (
	stuckWindowSize      = 6
	stuckRepeatThreshold = 3
)

// finishOutcome is how finishNode terminates the task.
type finishOutcome int

const (
	outcomeCompleted finishOutcome = iota
	outcomeFailed
	outcomeStopped
)

// GraphState holds all execution context for one task run.
type GraphState struct {
	// Static dependencies (set once, read-only during execution)
	taskID    string
	store     *Store
	llm       LLM
	validator *conductor.Validator
	exec      Execer
	gate      *Gate
	cfg       Config
	// parentTaskID is set when this state belongs to a sub-agent run, so its
	// emitted activity events nest under the orchestrator's handoff span.
	parentTaskID string

	// Mutable per-run state
	retryCtx        RetryContext
	memCtx          MemoryContext
	cmdWindow       []string           // command hashes for stuck detection (last stuckWindowSize)
	recentCmds      []string           // actual commands for stuck display (last stuckWindowSize)
	lastObs         string             // last observation for prompt injection (tag-wrapped)
	pendingAct      Action             // action buffered from stepNode for toolNode / approvalNode
	pendingProposal conductor.Proposal // validated proposal awaiting approval
	pendingCommand  string             // validated command currently executing (for observeResult)
	pendingHandle   ExecHandle         // running command handle while consulting (long_running)
	consultTag      string             // agent→LLM tag describing the consult condition
	consultBody     string             // consult content for the LLM
	consultCaller   string             // node that triggered the consult (for CONTINUE routing)
	lastErr         string             // error text for askNode to record in task.LastError
	lastSkillsSig   string             // last emitted skill set (de-dupes per-step skill_applied events)
	lastMemorySig   string             // last emitted memory signature (de-dupes memory_recalled events)
	finishOutcome   finishOutcome      // how finishNode terminates the task
	finishMsg       string             // summary (completed/stopped) or error (failed) for finishNode

	// consecutiveFailures counts back-to-back command failures; any success
	// resets it. At failureLimit() the failure observation escalates to a
	// forceful REPEATED_FAILURE directive; at 2×limit stepNode halts to the
	// operator. This replaces fingerprint pre-blocking: the model always sees
	// the real failure output and is pushed to adapt rather than being silently
	// blocked from retrying.
	consecutiveFailures int
}

// failureLimit is the number of consecutive command failures after which the
// agent escalates from a plain failure to a forceful REPEATED_FAILURE directive.
func (s *GraphState) failureLimit() int {
	if s.cfg.MaxConsecutiveFailures > 0 {
		return s.cfg.MaxConsecutiveFailures
	}
	return 3
}

// validate runs the proposal through the validator, returning an allow-all response when nil.
func (s *GraphState) validate(proposal conductor.Proposal) conductor.ChatResponse {
	if s.validator == nil {
		return conductor.ChatResponse{
			Intent:    "suggest_command",
			Proposals: []conductor.Proposal{proposal},
		}
	}
	return s.validator.ValidateChatResponse(conductor.ChatResponse{
		Intent:    "suggest_command",
		Proposals: []conductor.Proposal{proposal},
	})
}

// saveCheckpoint persists the current graph position to the task store.
func (s *GraphState) saveCheckpoint(node string) {
	_ = s.store.Update(s.taskID, func(t *Task) {
		t.Checkpoint = Checkpoint{
			TaskID:      s.taskID,
			CurrentNode: node,
			RetryCtx:    s.retryCtx,
			TokensUsed:  s.memCtx.TokensUsed,
			Summary:     s.memCtx.Summary,
			SavedAt:     time.Now().Unix(),
		}
		t.TokensUsed = s.memCtx.TokensUsed
	})
}

// commandHash returns an 8-hex-char fingerprint for duplicate/stuck detection.
// Whitespace is normalized first so trivial formatting jitter (a weak model
// re-emitting the same command with different spacing) still collapses to one
// fingerprint and is caught as a repeat.
func commandHash(cmd string) string {
	normalized := strings.Join(strings.Fields(cmd), " ")
	sum := sha256.Sum256([]byte(normalized))
	return fmt.Sprintf("%x", sum[:4])
}

// pushWindow appends a value to a sliding window capped at cap.
func pushWindow(window []string, value string, cap int) []string {
	window = append(window, value)
	if len(window) > cap {
		window = window[len(window)-cap:]
	}
	return window
}

// windowCount returns how many times value appears in the window.
func windowCount(window []string, value string) int {
	n := 0
	for _, v := range window {
		if v == value {
			n++
		}
	}
	return n
}

// isStuck checks the sliding window for ≥3 repeats or an A→B→A→B cycle.
func isStuck(window []string) bool {
	if len(window) < stuckRepeatThreshold {
		return false
	}
	counts := make(map[string]int, len(window))
	for _, h := range window {
		counts[h]++
		if counts[h] >= stuckRepeatThreshold {
			return true
		}
	}
	// Detect A→B→A→B cycle (minimum window length 4).
	if len(window) >= 4 {
		last := window[len(window)-4:]
		if last[0] == last[2] && last[1] == last[3] && last[0] != last[1] {
			return true
		}
	}
	return false
}

// markStepStatus records the outcome on the plan step the model referenced.
func markStepStatus(store *Store, taskID, stepID string, status StepStatus, detail string) {
	if stepID == "" {
		return
	}
	_ = store.Update(taskID, func(t *Task) {
		for i := range t.Plan {
			if t.Plan[i].ID == stepID {
				t.Plan[i].Status = status
				d := detail
				if len(d) > 200 {
					d = d[:200] + "…"
				}
				if status == StepFailed {
					t.Plan[i].Error = d
				} else {
					t.Plan[i].Result = d
				}
				return
			}
		}
	})
}

// observeResult verifies a finished command, records the tag-wrapped observation,
// updates the plan step, and accumulates findings. Returns edgeOK on success or
// edgeIsError on failure (both route back to the step node).
func observeResult(s *GraphState, result ToolResult) string {
	outcome, reason := Verify(result)
	if outcome == OutcomeBlocked {
		s.consecutiveFailures++
		body := s.pendingCommand + "\nreason: " + reason +
			"\nGateway rejected this target — it is out of network scope. " +
			"Choose a different, in-scope target, or ask the operator if you believe this is wrong."
		obs := Wrap("COMMAND_BLOCKED", body)
		s.memCtx.AddObservation(obs)
		s.lastObs = obs
		markStepStatus(s.store, s.taskID, s.pendingAct.StepID, StepFailed, reason)
		return edgeIsError
	}
	if outcome == OutcomeFailure {
		s.consecutiveFailures++
		body := s.pendingCommand + "\nreason: " + reason + "\n" + CompressObservation(result.Content)
		tag := "TOOL_FAILED"
		// Serious bill: once failures pile up, stop letting the model quietly
		// retry — demand a genuinely different approach (different tool/route,
		// `--help` to learn correct usage, a new plan, or finish/ask).
		if s.consecutiveFailures >= s.failureLimit() {
			tag = "REPEATED_FAILURE"
			body = fmt.Sprintf("%d commands have failed in a row. STOP repeating this approach. "+
				"Diagnose from the failure output below, then do ONE of: run a DIFFERENT tool or route, "+
				"inspect correct usage with `<tool> --help` or `-h`, update_plan with a new strategy, "+
				"or finish / ask_operator if truly blocked.\n\n%s", s.consecutiveFailures, body)
		}
		obs := Wrap(tag, body)
		s.memCtx.AddObservation(obs)
		s.lastObs = obs
		markStepStatus(s.store, s.taskID, s.pendingAct.StepID, StepFailed, reason)
		return edgeIsError
	}
	s.consecutiveFailures = 0 // any success clears the failure streak
	compressed := CompressObservation(result.Content)
	tag := "TOOL_RESPONSE"
	if compressed != result.Content {
		tag = "TOOL_RESPONSE_TRUNCATED"
	}
	obs := Wrap(tag, s.pendingCommand+"\n"+compressed)
	// Store the FULL output for SEND_FULL retrieval and summarization.
	s.memCtx.AddObservation(result.Content)
	s.lastObs = obs
	markStepStatus(s.store, s.taskID, s.pendingAct.StepID, StepDone, obs)
	s.emit(ActCommandDone, s.pendingCommand, compressed)
	if findings := filterEmpty(s.pendingAct.Findings); len(findings) > 0 || len(s.pendingAct.FindingsDetail) > 0 {
		_ = s.store.Update(s.taskID, func(t *Task) {
			t.Findings = append(t.Findings, findings...)
		})
		if task, ok := s.store.Get(s.taskID); ok {
			recordTaskFindings(context.Background(), s.cfg.Knowledge, task.SessionID, s.taskID, findings, s.pendingAct.FindingsDetail)
		}
		for _, f := range findings {
			s.emit(ActFinding, f, "")
		}
		for _, d := range s.pendingAct.FindingsDetail {
			if d.Title != "" {
				s.emit(ActFinding, d.Title, d.Description)
			}
		}
	}
	return edgeOK
}

// lastFullObservation returns the most recent full (uncompressed) tool output,
// used to answer a [SEND_FULL] request.
func lastFullObservation(s *GraphState) string {
	if n := len(s.memCtx.Observations); n > 0 {
		return s.memCtx.Observations[n-1]
	}
	return s.lastObs
}

// emit records a legible activity event for this task (best-effort, never
// fatal). Observational only — it never touches the execution path.
func (s *GraphState) emit(kind ActivityKind, summary, detail string) {
	if s.store == nil {
		return
	}
	task, _ := s.store.Get(s.taskID)
	s.store.RecordActivity(ActivityEvent{
		SessionID:    task.SessionID,
		TaskID:       s.taskID,
		ParentTaskID: s.parentTaskID,
		Kind:         kind,
		Summary:      summary,
		Detail:       detail,
	})
}

// Execute runs the state machine starting at startNode until a terminal node is reached.
// Returns a non-nil error only for unrecoverable infrastructure failures; task lifecycle
// errors (LLM failure, stuck) are handled inside nodes and leave the task in a clean state.
func Execute(ctx context.Context, s *GraphState, startNode string) error {
	current := startNode
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		fn, ok := nodes[current]
		if !ok {
			return fmt.Errorf("agent: unknown node %q", current)
		}
		edge, err := fn(ctx, s)
		if err != nil {
			return err
		}
		// RetryNode edgeRetryAgain uses dynamic routing back to the caller.
		if current == nodeRetry && edge == edgeRetryAgain {
			next := s.retryCtx.Caller
			if next == "" {
				return fmt.Errorf("agent: retry with no caller set")
			}
			s.saveCheckpoint(next)
			current = next
			continue
		}
		// Terminal detection: nodes absent from graphEdges (finishNode, askNode)
		// have no further transitions — stop regardless of the edge returned.
		edgeMap, hasEdges := graphEdges[current]
		if !hasEdges {
			return nil
		}
		nextNode, ok := edgeMap[edge]
		if !ok {
			return fmt.Errorf("agent: no edge %q from node %q", edge, current)
		}
		s.saveCheckpoint(nextNode)
		current = nextNode
	}
}

// filterEmpty removes empty strings from a slice.
func filterEmpty(ss []string) []string {
	out := ss[:0]
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}
