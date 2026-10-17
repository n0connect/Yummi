package agent

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/n0connect/yummi/orchestrator/internal/conductor"
	"github.com/n0connect/yummi/orchestrator/internal/security/threatscan"
)

// Node name constants.
const (
	nodeStep       = "step"
	nodeTool       = "tool"
	nodeConsult    = "consult"
	nodeRetry      = "retry"
	nodeSummarize  = "summarize"
	nodeApproval   = "approval"
	nodeFinish     = "finish"
	nodeAsk        = "ask"
	nodeHandoff    = "handoff"
	nodeRemember   = "remember"
	nodeSkillView  = "skill_view"
	nodeThreatHold = "threat_hold"
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
	edgeRemember         = "remember"
	edgeSkillView        = "skill_view"
	// Live-control edges (consult channel).
	edgeLongRunning = "long_running"
	edgeContinue    = "continue"
	edgeAbort       = "abort"
	edgeChangeGoal  = "change_goal"
	edgeStopTask    = "stop_task"
	edgeSendFull    = "send_full"
	edgeThreatHold  = "threat_hold"
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
		edgeRemember:    nodeRemember,
		edgeSkillView:   nodeSkillView,
		edgeLLMError:    nodeRetry,
	},
	nodeHandoff: {
		edgeOK: nodeStep,
	},
	nodeRemember: {
		edgeOK: nodeStep,
	},
	nodeSkillView: {
		edgeOK: nodeStep,
	},
	nodeTool: {
		edgeOK:               nodeStep,
		edgeIsError:          nodeStep,
		edgeRequiresApproval: nodeApproval,
		edgeStuck:            nodeConsult,
		edgeLongRunning:      nodeConsult,
		edgeThreatHold:       nodeThreatHold,
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
		edgeApproved:   nodeStep,
		edgeDenied:     nodeStep,
		edgeGiveUp:     nodeAsk,
		edgeOK:         nodeStep,
		edgeIsError:    nodeStep,
		edgeThreatHold: nodeThreatHold,
	},
	nodeThreatHold: {
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
		nodeStep:       stepNode,
		nodeTool:       toolNode,
		nodeConsult:    consultNode,
		nodeRetry:      retryNode,
		nodeSummarize:  summarizeNode,
		nodeApproval:   approvalNode,
		nodeFinish:     finishNode,
		nodeAsk:        askNode,
		nodeHandoff:    handoffNode,
		nodeRemember:   rememberNode,
		nodeSkillView:  skillViewNode,
		nodeThreatHold: threatHoldNode,
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
	// autoFinishTerminalPlan is enabled by Runner-built production states. It
	// remains explicit on GraphState so low-level node tests can exercise loop
	// guards without being short-circuited by orchestration-level convergence.
	autoFinishTerminalPlan bool

	// Mutable per-run state
	retryCtx RetryContext
	// overflowCompactions counts context-window overflows this task has answered
	// by forcing compaction. Bounded so an overflow that compaction cannot cure
	// (e.g. the brief alone is too large) cannot loop forever.
	overflowCompactions int
	memCtx              MemoryContext
	cmdWindow           []string           // command hashes for stuck detection (last stuckWindowSize)
	recentCmds          []string           // actual commands for stuck display (last stuckWindowSize)
	executedCmds        []string           // commands actually dispatched (blocked proposals excluded)
	lastObs             string             // last observation for prompt injection (tag-wrapped)
	pendingAct          Action             // action buffered from stepNode for toolNode / approvalNode
	pendingProposal     conductor.Proposal // validated proposal awaiting approval
	pendingCommand      string             // validated command currently executing (for observeResult)
	pendingHandle       ExecHandle         // running command handle while consulting (long_running)
	consultTag          string             // agent→LLM tag describing the consult condition
	consultBody         string             // consult content for the LLM
	consultCaller       string             // node that triggered the consult (for CONTINUE routing)
	lastErr             string             // error text for askNode to record in task.LastError
	lastMemorySig       string             // last emitted memory signature (de-dupes memory_recalled events)
	finishOutcome       finishOutcome      // how finishNode terminates the task
	finishMsg           string             // summary (completed/stopped) or error (failed) for finishNode

	pendingThreatResult ToolResult // quarantined threat tool result awaiting approval
	// consecutiveFailures counts back-to-back command failures; any success
	// resets it. At failureLimit() the failure observation escalates to a
	// forceful REPEATED_FAILURE directive; at 2×limit stepNode halts to the
	// operator. This replaces fingerprint pre-blocking: the model always sees
	// the real failure output and is pushed to adapt rather than being silently
	// blocked from retrying.
	consecutiveFailures int

	// incompleteFinishNudges counts how many times a premature FINISH (emitted
	// while plan steps are still pending) has been bounced back with an
	// INCOMPLETE_GOALS observation. Capped so a model that insists on finishing
	// after being shown the remaining goals is ultimately obeyed rather than
	// wedged — the guard buys exactly one forced reconsideration per unmet-goal
	// situation, model-size-agnostic, not an infinite block.
	incompleteFinishNudges int
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
	observations := s.memCtx.Observations
	if len(observations) > 6 {
		observations = observations[len(observations)-6:]
	}
	boundedObservations := make([]string, len(observations))
	for i, observation := range observations {
		boundedObservations[i] = CompressObservation(observation)
	}
	_ = s.store.Update(s.taskID, func(t *Task) {
		t.Checkpoint = Checkpoint{
			TaskID:              s.taskID,
			CurrentNode:         node,
			StepIndex:           t.StepsUsed,
			RetryCtx:            s.retryCtx,
			TokensUsed:          s.memCtx.TokensUsed,
			Summary:             s.memCtx.Summary,
			LastObservation:     CompressObservation(s.lastObs),
			RecentCommands:      append([]string(nil), s.recentCmds...),
			ExecutedCommands:    append([]string(nil), s.executedCmds...),
			MemoryObservations:  boundedObservations,
			ConsecutiveFailures: s.consecutiveFailures,
			SavedAt:             time.Now().Unix(),
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

// toolThrashThreshold is how many times a single TOOL may appear in the command
// window before the agent nudges the model. It is deliberately above 3 so a
// legitimate escalation on one tool (e.g. nmap -sV → nmap -p- → nmap -sC) is not
// flagged, while a wider rotation of trivially-different invocations of the same
// tool (ps -ef / ps -ejH / ps aux / ps -e) — which exact-hash stuck detection
// misses because each string is distinct — is caught.
const toolThrashThreshold = 4

// toolThrash reports the tool a window is thrashing on: the same first token
// (tool name) appearing >= toolThrashThreshold times. Returns ("", false) when
// no tool dominates. This catches semantic near-duplicate loops that commandHash
// (exact-match) cannot, since "ps -ef" and "ps -ejH" hash differently but are
// the same tool producing the same class of information.
func toolThrash(commands []string) (string, bool) {
	counts := make(map[string]int, len(commands))
	for _, c := range commands {
		fields := strings.Fields(c)
		if len(fields) == 0 {
			continue
		}
		tool := fields[0]
		counts[tool]++
		if counts[tool] >= toolThrashThreshold {
			return tool, true
		}
	}
	return "", false
}

// markStepStatus records the outcome (and the actual command run, if any) on
// the plan step the model referenced. If stepID is empty or doesn't match any
// plan step — the prompt shows every step's ID clearly, but a model's
// compliance can't be guaranteed, and this was observed live: a completed
// task with real findings still showed every plan step frozen at "pending"
// because the model's step_id never matched — it falls back to the first
// step still Pending or Running, so the plan view tracks real progress
// instead of silently showing nothing happened. Recording the command lets
// finishNode later cross-check the model's own claimed findings against
// which tools' commands actually completed (see unverifiedToolWarning) —
// found necessary live: a model reported "nikto detected vulnerabilities"
// after nikto was aborted twice with zero real output.
func markStepStatus(store *Store, taskID, stepID, command string, status StepStatus, detail string) {
	_ = store.Update(taskID, func(t *Task) {
		idx := -1
		for i := range t.Plan {
			if t.Plan[i].ID == stepID {
				idx = i
				break
			}
		}
		if idx == -1 {
			for i := range t.Plan {
				if t.Plan[i].Status == StepPending || t.Plan[i].Status == StepRunning {
					idx = i
					break
				}
			}
		}
		if idx == -1 {
			return
		}
		t.Plan[idx].Status = status
		if command != "" {
			t.Plan[idx].Command = command
		}
		d := detail
		// 500, not the 120 the prompt renderer trims to — this is the copy
		// verifyFindingDetail checks a FINISH-time finding's cited evidence
		// against, so keeping more of it lets a real fact quoted from later
		// in the output still verify instead of clipping to "unsupported".
		if len(d) > 500 {
			d = d[:500] + "…"
		}
		if status == StepFailed {
			t.Plan[idx].Error = d
		} else {
			t.Plan[idx].Result = d
		}
	})
}

// unverifiedToolWarning cross-checks the model's own claimed findings against
// which tools' plan steps actually completed. Found necessary via live
// testing: a model reported "nikto detected known vulnerabilities" for a
// nikto invocation that was aborted TWICE with zero real output — the prompt-
// level instruction to only report grounded findings did not reliably
// prevent this. Deliberately does NOT strip or rewrite the finding — only the
// operator can judge whether a specific claim is actually accurate; this just
// makes the risk visible instead of silently trusting (or silently
// censoring) the model's own report.
func unverifiedToolWarning(task Task, findings []string) string {
	if len(findings) == 0 {
		return ""
	}
	lowerFindings := make([]string, len(findings))
	for i, f := range findings {
		lowerFindings[i] = strings.ToLower(f)
	}

	var suspect []string
	seen := map[string]bool{}
	for _, step := range task.Plan {
		if step.Status != StepFailed || step.Command == "" {
			continue
		}
		fields := strings.Fields(step.Command)
		if len(fields) == 0 {
			continue
		}
		tool := strings.ToLower(fields[0])
		if seen[tool] {
			continue
		}
		for _, f := range lowerFindings {
			if strings.Contains(f, tool) {
				seen[tool] = true
				suspect = append(suspect, tool)
				break
			}
		}
	}
	if len(suspect) == 0 {
		return ""
	}
	sort.Strings(suspect)
	return fmt.Sprintf(
		"⚠ UNVERIFIED: the findings above reference %s, but the corresponding command did not complete successfully (failed/aborted) — treat any claimed result from it as unconfirmed until manually re-run.",
		strings.Join(suspect, ", "),
	)
}

// observeResult verifies a finished command, records the tag-wrapped observation,
// updates the plan step, and accumulates findings. Returns edgeOK on success or
// edgeIsError on failure (both route back to the step node).
func observeResult(s *GraphState, result ToolResult) string {
	// Strip ANSI/C1 escape sequences before the output enters the prompt and the
	// observation log: interactive tools (nmap, gobuster) emit color/cursor codes
	// that pollute the model's context and are a terminal/log-injection vector.
	// Done once here so every downstream use (failure body, success body, the
	// SEND_FULL buffer, the threat scan) sees clean text. The raw bytes remain in
	// the execution store's audit record.
	result.Content = threatscan.StripANSI(result.Content)
	outcome, reason := Verify(result)
	if outcome == OutcomeBlocked {
		s.consecutiveFailures++
		body := s.pendingCommand + "\nreason: " + reason +
			"\nGateway rejected this target — it is out of network scope. " +
			"Choose a different, in-scope target, or ask the operator if you believe this is wrong."
		return s.failStep("COMMAND_BLOCKED", body, reason)
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
		return s.failStep(tag, body, reason)
	}
	s.consecutiveFailures = 0 // any success clears the failure streak
	// Content-layer fail-closed: check if raw output trips known threat patterns.
	// If a threat is detected, quarantine the output and transition to threat hold node.
	if banner := threatscan.Banner(result.Content); banner != "" {
		slog.Warn("agent: tool output tripped threat patterns, quarantining",
			"task", s.taskID, "command", s.pendingCommand)
		s.pendingThreatResult = result
		s.pendingProposal = conductor.Proposal{
			ID:        uuid.NewString(),
			Command:   s.pendingCommand,
			Rationale: "Threat Scan flagged potential injection in output",
			RiskLevel: "threat_approval",
		}
		return edgeThreatHold
	}

	compressed := CompressObservation(result.Content)
	tag := "TOOL_RESPONSE"
	if compressed != result.Content {
		tag = "TOOL_RESPONSE_TRUNCATED"
	}
	body := s.pendingCommand + "\n" + compressed
	obs := Wrap(tag, body)
	// Store the FULL output for SEND_FULL retrieval and summarization.
	s.memCtx.AddObservation(result.Content)
	s.lastObs = obs
	markStepStatus(s.store, s.taskID, s.pendingAct.StepID, s.pendingCommand, StepDone, obs)
	s.emit(ActCommandDone, "Command result processed", compressed)
	if findings := SanitizeFindings(s.pendingAct.Findings); len(findings) > 0 || len(s.pendingAct.FindingsDetail) > 0 {
		task, ok := s.store.Get(s.taskID)
		var records []FindingRecord
		if ok {
			records = buildFindingRecords(s.pendingAct.FindingsDetail, s.pendingAct.StepID, result.Content, task.Plan)
		}
		_ = s.store.Update(s.taskID, func(t *Task) {
			t.Findings = append(t.Findings, findings...)
			t.FindingRecords = append(t.FindingRecords, records...)
		})
		if ok {
			recordTaskFindings(context.Background(), s.cfg.Knowledge, task.SessionID, s.taskID, findings, s.pendingAct.FindingsDetail)
		}
		for _, f := range findings {
			s.emit(ActFinding, f, "")
		}
		for _, d := range s.pendingAct.FindingsDetail {
			if d.Title != "" {
				s.emit(ActFinding, SanitizeAgentText(d.Title), SanitizeAgentText(d.Description))
			}
		}
	}
	return edgeOK
}

// failStep records a tag-wrapped failure observation, marks the current plan
// step failed, and returns the error edge — the shared tail for
// observeResult's OutcomeBlocked and OutcomeFailure branches, which differ
// only in how tag/body are built above.
func (s *GraphState) failStep(tag, body, reason string) string {
	obs := Wrap(tag, body)
	s.memCtx.AddObservation(obs)
	s.lastObs = obs
	markStepStatus(s.store, s.taskID, s.pendingAct.StepID, s.pendingCommand, StepFailed, reason)
	return edgeIsError
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
