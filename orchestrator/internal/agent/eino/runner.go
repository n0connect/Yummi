package eino

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/n0connect/yummi/orchestrator/internal/agent"
	"github.com/n0connect/yummi/orchestrator/internal/conductor"
	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
	"github.com/n0connect/yummi/orchestrator/internal/skill"
	"github.com/n0connect/yummi/orchestrator/internal/skillcurator"
)

// Config mirrors agent.Config's shape for the fields this backend actually
// uses. Retry-backoff tuning and the graph engine's stuck/duplicate/tool-thrash
// loop-prevention heuristics remain graph-only — they're tied to the graph's
// rebuilt-every-step state, not to
// "agent/memory/skill" capability; MaxIterations (wired from StepBudget,
// below) is this backend's analogous safety net. Everything else — memory
// retrieval, skill injection, sub-agent delegation, auto-skill-creation — has
// full parity with the graph backend.
type Config struct {
	StepBudget   int
	ApprovalMode agent.ApprovalMode
	ModelBaseURL string // e.g. "http://localhost:11434/v1" or "https://openrouter.ai/api/v1"
	ModelName    string // e.g. "anthropic/claude-3.5-sonnet" or "gpt-oss:20b"
	ModelAPIKey  string // e.g. "sk-or-v1-..." or empty for local Ollama
	// ModelTimeout bounds every individual outbound call to the model — see
	// NewOllamaChatModel's doc comment for why this exists (its absence let a
	// single stalled model call hang a task's goroutine forever, found via
	// live long-form testing). <=0 falls back to a generous default.
	ModelTimeout time.Duration
	// ModelNumCtx is injected into every outbound call as Ollama's
	// options.num_ctx — see NewOllamaChatModel's doc comment. Should be the
	// same effective value conductor.NewEngineWithOverrides resolved for the
	// configured tier/override, so both backends agree on context size.
	// <=0 omits the field (server default, likely too small).
	ModelNumCtx int
	// ModelThinking mirrors the resolved tier/override Thinking value the
	// conductor already applies to its own calls — see NewOllamaChatModel's
	// doc comment for why false maps to "medium" reasoning_effort here, not
	// a literal off (gpt-oss has no off switch, and this backend targets
	// harder agentic work than the conductor's chat/intent classification).
	ModelThinking bool
	// PlanLLM is a plain-chat client used ONLY for the narrow, single-purpose
	// goal-part audit (agent.AuditGoalParts) that backs the premature-FINISH
	// guard's natural-language fallback below — separate from cm, the Eino
	// ChatModel that actually drives the ReAct tool-calling loop. Reuses
	// whichever agent.LLM the graph backend's own step loop uses (main.go
	// wires the same value both places) so the audit call inherits the
	// already tier-resolved num_ctx/fallback/circuit-breaker behavior instead
	// of standing up a second, differently-configured client for one short
	// call. Nil disables the natural-language fallback — SplitEnumeratedParts'
	// numbered-marker case still works without it.
	PlanLLM agent.LLM
	// Persona is the same document-driven agent.yaml identity/principles the
	// graph backend renders via StepSystemPrompt (ORIENT/ACT/OBSERVE/CONCLUDE:
	// NO FABRICATION, EVIDENCE, TRUTH, COMPLETE, ...). Previously this backend
	// had no equivalent at all — instruction() was a hardcoded string covering
	// only tool-routing mechanics, so none of agent.yaml's epistemic-discipline
	// guardrails ever reached the model here, live-model-only failure modes
	// (accepting an unverified operator premise as fact, inventing command
	// output that never ran, denying an actual hallucination) that the graph
	// backend's own persona explicitly guards against. Zero-value Persona
	// falls back to a minimal identity line so existing tests that don't set
	// it keep working.
	Persona   agent.AgentPersona
	Knowledge *knowledge.Store
	// MCPExec runs the six cataloged pentest tools via the sandbox-resident
	// MCP server. nil disables those tools (main.go always wires a real one
	// today; kept optional so tests can omit it).
	MCPExec agent.MCPExecer
	// MCPList backs search_pentest_tools' discovery of the full sandbox-mcp
	// catalog (including tools beyond the six individually-typed ones). nil
	// disables search_pentest_tools/run_pentest_tool; main.go wires the same
	// *sandboxmcp.Client used for MCPExec's underlying calls here too.
	MCPList mcpLister

	// Retrieval selects prior memory folded into the task's Instruction once,
	// at Run() start. Nil disables memory injection. Typically
	// knowledge.NewStructuredRetrieval(Knowledge) / NewSemanticRetrieval.
	Retrieval knowledge.RetrievalStrategy
	// Skills is the declarative playbook registry folded into Instruction the
	// same way. Nil disables skill injection.
	Skills *skill.Registry
	// SubAgents maps a role name to a specialized sub-agent Runner the model
	// may hand a focused subtask to via the handoff tool. Empty/nil disables
	// delegation. Sub-agents are always graph Runners regardless of this
	// backend — see (*agent.Runner).RunSubtask's doc comment.
	SubAgents map[string]*agent.Runner
	// SubAgentHints maps a role name (same keys as SubAgents) to a short
	// routing-hint sentence, mirroring agent.Config.SubAgentHints — rendered
	// into the handoff tool's description alongside each role name.
	SubAgentHints map[string]string
	// Delegations bounds sub-agent concurrency and tracks addressable runs.
	// Required (together with a non-empty SubAgents) for the handoff tool to
	// be registered at all.
	Delegations *agent.DelegationCoordinator
	// SubAgentTimeout is the hard ceiling on one handoff sub-agent run
	// (default 10m).
	SubAgentTimeout time.Duration
	// SkillCurator, if set, is invoked asynchronously after a task finishes
	// successfully, to propose a candidate skill drafted from its trajectory
	// — the same non-blocking hook graph.go's finishNode fires. Nil disables
	// auto-skill-creation.
	SkillCurator func(ctx context.Context, traj skillcurator.Trajectory)
}

// EinoRunner drives agentic tasks via Eino's ADK instead of the graph state
// machine. It implements agent.TaskRunner so internal/api is unaware of
// which backend cmd/orchestrator/main.go wired at startup.
type EinoRunner struct {
	store     *agent.Store
	validator *conductor.Validator
	exec      agent.Execer
	gate      *agent.Gate
	cfg       Config

	mu     sync.Mutex
	active map[string]context.CancelFunc
	// resumeMsg holds the operator's answer for a task about to resume from
	// StatusPaused, keyed by task ID and consumed once by Run(). Eino's
	// ChatModelAgent keeps its conversation state in-memory only for the
	// duration of one Run() call — nothing persists it — so a resumed run
	// necessarily restarts the conversation; this at least carries the
	// question/answer forward instead of the model resuming with no idea it
	// ever asked anything. Full mid-conversation resume would need Eino's own
	// checkpoint/serialization support. Startup recovery is therefore
	// reconstructive (durable goal/plan + explicit recovery guidance), not a
	// claim that native token history was restored.
	resumeMsg map[string]string
}

// NewEinoRunner wires the loop driver. validator may be nil (allow-all
// classification — tests only, matching agent.NewRunner's own contract).
func NewEinoRunner(store *agent.Store, validator *conductor.Validator, exec agent.Execer, cfg Config) *EinoRunner {
	if cfg.StepBudget <= 0 {
		cfg.StepBudget = 15
	}
	if cfg.ApprovalMode == "" {
		cfg.ApprovalMode = agent.ModeAskPermission
	}
	if cfg.SubAgentTimeout <= 0 {
		cfg.SubAgentTimeout = 10 * time.Minute
	}
	// Mirrors agent.NewRunner's own auto-creation exactly (default quota 3);
	// only one backend is ever active in a given process (main.go's
	// YUMMI_AGENT_BACKEND switch is mutually exclusive), so there's no
	// reason to share one coordinator across backends.
	if len(cfg.SubAgents) > 0 && cfg.Delegations == nil {
		cfg.Delegations = agent.NewDelegationCoordinator(3)
	}
	return &EinoRunner{
		store: store, validator: validator, exec: exec,
		gate: agent.NewGate(), cfg: cfg,
		active:    make(map[string]context.CancelFunc),
		resumeMsg: make(map[string]string),
	}
}

// ── agent.Agent ──────────────────────────────────────────────────────────

func (r *EinoRunner) Name() string { return "yummi-eino" }

// Handoffs returns the configured sub-agents as the delegation set, or nil
// when none are configured — mirrors graph.Runner.Handoffs exactly, since
// sub-agents are always graph Runners regardless of which backend drives the
// top-level task.
func (r *EinoRunner) Handoffs() map[string]agent.Agent {
	if len(r.cfg.SubAgents) == 0 {
		return nil
	}
	out := make(map[string]agent.Agent, len(r.cfg.SubAgents))
	for role, sub := range r.cfg.SubAgents {
		out[role] = sub
	}
	return out
}

// unknownToolHandler recovers from a model calling a tool name Eino's
// ToolsNode has no registration for, instead of letting that abort the whole
// task (see the ToolsNodeConfig wiring above for the incident that motivated
// this). name is exactly the Function.Name the model emitted; the returned
// string is fed back as that tool call's own result, so the model sees it on
// its very next turn like any other tool observation.
func unknownToolHandler(_ context.Context, name, _ string) (string, error) {
	return fmt.Sprintf(
		"UNKNOWN_TOOL: %q is not a directly-callable tool. If it's one of the cataloged "+
			"sandbox pentest tools, call run_pentest_tool with tool=%q and the matching args "+
			"instead — do not call %q as a function name. If you're not sure it exists or what "+
			"arguments it takes, call search_pentest_tools first.",
		name, name, name,
	), nil
}

func (r *EinoRunner) Run(ctx context.Context, task *agent.Task) error {
	t, ok := r.store.Get(task.ID)
	if !ok {
		return fmt.Errorf("%w: %s", agent.ErrTaskNotFound, task.ID)
	}
	if t.Status != agent.StatusPlanned && t.Status != agent.StatusPaused {
		return fmt.Errorf("task %s is %s; expected planned or paused", task.ID, t.Status)
	}
	priorQuestion := t.Question
	wasPaused := t.Status == agent.StatusPaused
	r.mu.Lock()
	operatorAnswer := r.resumeMsg[task.ID]
	delete(r.resumeMsg, task.ID)
	r.mu.Unlock()
	if err := r.store.Transition(task.ID, agent.StatusRunning, func(t *agent.Task) {
		t.Question = ""
	}); err != nil {
		return err
	}

	cm, err := NewOllamaChatModel(ctx, r.cfg.ModelBaseURL, r.cfg.ModelName, r.cfg.ModelAPIKey, r.cfg.ModelTimeout, r.cfg.ModelNumCtx, r.cfg.ModelThinking)
	if err != nil {
		_ = r.store.Transition(task.ID, agent.StatusFailed, func(t *agent.Task) { t.LastError = err.Error() })
		return err
	}

	// trace/capture collect signal Run()'s event loop can't see directly:
	// Eino's native tool-calling keeps its own chat history rather than the
	// step-by-step observation log graph.go's MemoryContext builds, and the
	// event stream only surfaces the final chat message, not a tool call's
	// own structured arguments.
	trace := &runTrace{}
	capture := &finishCapture{}
	ask := &askCapture{}

	rcDeps := runCommandDeps{
		taskID: task.ID, sessionID: t.SessionID, store: r.store, exec: r.exec, gate: r.gate,
		validator: r.validator, approvalMode: func() agent.ApprovalMode { return r.cfg.ApprovalMode },
		trace: trace,
	}
	tools := []tool.BaseTool{
		newRunCommandTool(rcDeps),
		newRememberTool(rememberDeps{taskID: task.ID, sessionID: t.SessionID, knowledge: r.cfg.Knowledge, origin: "agent"}),
		newAskOperatorTool(ask),
		newFinishTool(capture),
		newUpdatePlanTool(r.store, task.ID),
	}
	if r.cfg.MCPExec != nil {
		pentestDeps := pentestToolDeps{
			taskID: task.ID, sessionID: t.SessionID, store: r.store, mcpExec: r.cfg.MCPExec,
			gate: r.gate, validator: r.validator, approvalMode: func() agent.ApprovalMode { return r.cfg.ApprovalMode },
			trace: trace,
		}
		for _, spec := range pentestToolSpecs {
			tools = append(tools, newPentestTool(spec, pentestDeps))
		}
		// Terminal-session verbs share the exact same pentestTool machinery;
		// their catalog entries mark them target-free (see terminal_tools.go).
		for _, spec := range terminalToolSpecs {
			tools = append(tools, newPentestTool(spec, pentestDeps))
		}
		if r.cfg.MCPList != nil {
			tools = append(tools, newSearchPentestToolsTool(r.cfg.MCPList), newRunPentestTool(pentestDeps))
		}
	}
	if len(r.cfg.SubAgents) > 0 && r.cfg.Delegations != nil {
		tools = append(tools, newHandoffTool(handoffDeps{
			taskID: task.ID, sessionID: t.SessionID, store: r.store,
			subAgents: r.cfg.SubAgents, hints: r.cfg.SubAgentHints,
			delegations: r.cfg.Delegations, timeout: r.cfg.SubAgentTimeout,
		}))
	}

	// MEMORY/SKILLS are folded into the static Instruction once, up front —
	// see buildMemoryBlock's doc comment for why a one-time injection is the
	// correct analogue of graph.go's per-step re-injection for a backend that
	// already keeps native chat history.
	instr := r.instruction(t.Goal)
	if mem := buildMemoryBlock(ctx, r.cfg.Retrieval, t.SessionID, t.Goal); mem != "" {
		instr += "\n\n" + mem
		r.store.RecordActivity(agent.ActivityEvent{
			SessionID: t.SessionID, TaskID: task.ID, Kind: agent.ActMemory, Summary: "Relevant session context applied",
		})
	}
	if skillBlock, names := buildSkillsBlock(r.cfg.Skills, t.Goal); skillBlock != "" {
		instr += "\n\n" + skillBlock
		r.store.RecordActivity(agent.ActivityEvent{
			SessionID: t.SessionID, TaskID: task.ID, Kind: agent.ActSkill, Summary: strings.Join(names, ", "),
		})
	}

	// enumParts is computed here — BEFORE agent construction, not at its
	// original spot right before the retry loop — specifically so it can
	// also size effectiveStepBudget below. It still drives the premature-
	// FINISH guard the same way once the retry loop starts.
	//
	// A multi-part goal ("1) ... 2) ... 3) ..." or "scan X and also check
	// Y") reproducibly needs MUCH more real headroom than the flat
	// StepBudget default (15) provides, and this was proven live, not
	// guessed: three separate real runs against an actual target, using
	// real cataloged pentest tools, either exhausted the entire 15-step
	// budget or came within one step of it — including a run with only TWO
	// requested parts (gobuster + nikto) that never even reached the second
	// part, because ONE tool hitting real-world friction (a missing
	// wordlist, in that case) was enough to burn the whole budget on its
	// own. A flat higher default doesn't fit either: a genuinely
	// single-intent goal ("what OS is this") finishes correctly in 2-4 real
	// steps today and gains nothing from a bigger ceiling except a weaker
	// runaway-loop backstop (MaxIterations is this backend's ONLY loop-
	// prevention mechanism — see Config's own doc comment on why the
	// graph's stuck/duplicate heuristics don't apply here). Scaling with the
	// goal's OWN detected complexity is the proportionate fix: budget grows
	// only when there's real reason to expect it's needed.
	enumParts := agent.SplitEnumeratedParts(t.Goal)
	if len(enumParts) < 2 && r.cfg.PlanLLM != nil && agent.LikelyMultiPart(t.Goal) {
		if audited := agent.AuditGoalParts(ctx, r.cfg.PlanLLM, t.Goal); len(audited) >= 2 {
			enumParts = audited
		}
	}
	// See agent.ScaledStepBudget's own doc comment for the formula and the
	// live evidence behind it — shared with the graph backend so a scaling
	// change only needs to happen once.
	effectiveStepBudget := agent.ScaledStepBudget(r.cfg.StepBudget, len(enumParts))
	if effectiveStepBudget != task.StepBudget {
		_ = r.store.Update(task.ID, func(t *agent.Task) { t.StepBudget = effectiveStepBudget })
	}

	// enumParts was, until now, used ONLY as bookkeeping: it sized the step
	// budget above and (later, in the retry loop) audits coverage at the
	// moment the model tries to call finish. The model itself never saw it —
	// Instruction only ever carried the raw goal prose (below), so a goal
	// asking for several distinct things relied entirely on the model's own
	// single read of that prose, across a multi-turn tool-calling loop, to
	// remember and prioritize all of them. Live-observed: a goal detected as
	// having 6 real parts (full pentest: enumerate, fingerprint, dir
	// discovery, vuln scan, credential attack, exploit) got 2 of them
	// attempted before the model called finish — the SAME under-detection
	// that under-sized the budget above also meant the finish-time audit
	// judged its own too-short list as covered. Folding the detected parts
	// into the static Instruction, same one-time-injection pattern as
	// MEMORY/SKILLS above, keeps every part visible to the model on every
	// turn instead of leaving it to native chat history alone — the
	// cheapest fix that doesn't touch the audit-at-finish mechanism itself.
	if len(enumParts) >= 2 {
		var checklist strings.Builder
		checklist.WriteString("This goal has multiple distinct parts. Address EACH one — do not call finish until every part below has been genuinely attempted, not just the first one or two:\n")
		for i, part := range enumParts {
			fmt.Fprintf(&checklist, "%d. %s\n", i+1, part)
		}
		instr += "\n\n" + checklist.String()
		r.store.RecordActivity(agent.ActivityEvent{
			SessionID: t.SessionID, TaskID: task.ID, Kind: agent.ActPlan,
			Summary: fmt.Sprintf("%d goal parts detected — tracking each in the task instruction", len(enumParts)),
		})
	}

	einoAgent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "yummi",
		Description: "an autonomous penetration-testing operator assistant",
		Instruction: instr,
		Model:       cm, // already model.ToolCallingChatModel per NewOllamaChatModel's return type
		// StepBudget's real enforcement: without this, Eino's own default
		// (20) governs regardless of what the task was actually configured
		// for. effectiveStepBudget (not the flat r.cfg.StepBudget) — see its
		// own computation above for why a multi-part goal needs more room.
		MaxIterations: effectiveStepBudget,
		ToolsConfig: adk.ToolsConfig{
			// ExecuteSequentially: eino's default runs every tool call in a
			// model turn concurrently. agent.Gate is single-slot per task
			// (see its doc comment: "one pending approval per task, the loop
			// is sequential") — two concurrent ask_permission gates on the
			// same task race Gate.Request, silently orphaning the loser's
			// verdict channel while the Task's persisted PendingProposalID
			// reflects whichever store.Transition happened to land last, so
			// POST /tasks/{id}/approve can 404 with "no pending approval"
			// even when the operator is approving exactly the proposal the
			// UI just showed them. Sequential dispatch keeps tool execution
			// (and therefore gating) one-at-a-time, honoring Gate's existing
			// invariant instead of turning it into a multi-slot structure.
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools: tools, ExecuteSequentially: true,
				// Without this, a model calling a tool name that isn't
				// actually registered — confirmed live: DeepSeek, mid-task,
				// called "nuclei_scan" directly instead of routing it
				// through run_pentest_tool(tool="nuclei_scan", ...) as its
				// own catalog-discovery tool requires — hits Eino's
				// framework-level "tool X not found in toolsNode indexes"
				// error, which aborts the ENTIRE task rather than letting
				// the model see its own mistake and self-correct. A single
				// hallucinated tool name should cost one turn, not the
				// whole run. This feeds the mistake back as an ordinary
				// tool result instead, naming the two real recovery paths
				// (run_pentest_tool for a catalog tool, search_pentest_tools
				// if the exact name/schema is unknown).
				UnknownToolsHandler: unknownToolHandler,
			},
			// finish/ask_operator are terminal control signals, not tools whose
			// result should be fed back for another model turn — without this,
			// the loop only stops once the model itself decides not to call
			// another tool, which is neither guaranteed nor bounded below
			// MaxIterations. ReturnDirectly makes the framework end the turn
			// the instant either is called, matching graph.go's FINISH/
			// ASK_OPERATOR actions, which are terminal edges in the state
			// machine by construction.
			ReturnDirectly: map[string]bool{finishInfo.Name: true, askOperatorInfo.Name: true},
		},
	})
	if err != nil {
		_ = r.store.Transition(task.ID, agent.StatusFailed, func(t *agent.Task) { t.LastError = err.Error() })
		return err
	}

	runCtx, cancel := context.WithCancel(ctx)
	r.mu.Lock()
	r.active[task.ID] = cancel
	r.mu.Unlock()
	defer func() {
		cancel()
		r.mu.Lock()
		delete(r.active, task.ID)
		r.mu.Unlock()
	}()

	// A resume from StatusPaused starts a brand-new conversation (see
	// resumeMsg's doc comment on why) — folding the question/answer into the
	// opening message is the best continuity available without checkpointing,
	// and specifically avoids the model immediately re-asking the same
	// question with no memory of having asked it.
	initialGoal := t.Goal
	if wasPaused {
		answer := strings.TrimSpace(operatorAnswer)
		if answer == "" {
			answer = "(no additional guidance given; use your own judgment and continue)"
		}
		if priorQuestion != "" {
			initialGoal = fmt.Sprintf(
				"Resuming task toward the original goal: %s\n\nYou previously paused and asked the operator: %q\nOperator's answer: %s",
				t.Goal, priorQuestion, answer)
		} else {
			initialGoal = fmt.Sprintf("Resuming task toward the original goal: %s\n\nRecovery guidance: %s", t.Goal, answer)
		}
	}

	runner := adk.NewRunner(runCtx, adk.RunnerConfig{Agent: einoAgent})

	// enumParts (computed above, before agent construction) also drives the
	// premature-FINISH guard (parity with graph.go's stepNode guard): a
	// multi-part goal is the exact case where the model reproducibly answers
	// the first part and then calls finish, leaving the rest untouched — and
	// this backend has no plan/step backbone to catch it. When finish is
	// reached but fewer distinct tool results were produced than the goal
	// has parts, bounce ONCE: re-run the agent with the parts listed and the
	// work-so-far carried forward, so it completes the remaining parts.
	// Bounded to a single re-run — a model that finishes again after seeing
	// the checklist is obeyed, never wedged. This is model-size-independent:
	// the deterministic bounce, not a bigger model, is what forces the
	// second attempt.
	openingMessage := initialGoal
	nudgeCount := 0
	const maxNudges = 3
	transientRetries := 0
	// nudgeCount replaces a first-attempt-only bool: the original single-shot
	// nudge was NOT structurally enforced, only requested by its own text —
	// live-observed nudged once, the model tried finish again one turn
	// later having run exactly zero new tools, and it was silently accepted
	// purely because "already nudged" was true. A first fix tried gating the
	// re-accept on "did trace.pairs() grow at all since the last nudge" —
	// also live-observed to be too weak: a SECOND live run started with
	// pairs=0 (the model's very first move was finish with nothing done at
	// all), got nudged, then ran exactly two things — one failing `ls`, one
	// real nmap scan — and immediately tried finish again with 4 of 6 parts
	// still untouched. pairs went 0→2, so "did pairs grow" said yes, and the
	// still-badly-incomplete finish sailed through anyway. Growth in
	// trace.pairs() is not the same claim as "coverage actually improved" —
	// only re-running the real coverage audit proves that. So every
	// believesFinished occurrence (not just the first) gets audited the same
	// way, up to maxNudges total, and only a fully-covered result (or
	// exhausting the cap) is allowed through — matching the exact
	// reliability bar as the first nudge, applied uniformly instead of once.
	// Isolated live reproduction of the exact single-nudge scenario (same
	// system instruction, same "work already completed" list, asked
	// directly, 3/3 trials) reliably picked the right next tool every time —
	// so the gap was never the model being unable to reason through the
	// checklist, only that nothing here actually verified it had.

	var summary string
	var findings []string
	for {
		capture.reset()
		ask.reset()

		iter := runner.Run(runCtx, []*schema.Message{schema.UserMessage(openingMessage)})
		var lastMessage *schema.Message
		var runErr error
		for {
			event, ok := iter.Next()
			if !ok {
				break
			}
			if event.Err != nil {
				runErr = event.Err
			}
			if event.Output != nil && event.Output.MessageOutput != nil && !event.Output.MessageOutput.IsStreaming {
				lastMessage = event.Output.MessageOutput.Message
				// One completed (non-streaming) Role==Assistant MessageOutput
				// == one real ChatModel-node turn == exactly what Eino's own
				// adk.ChatModelAgentConfig.MaxIterations decrements against
				// (see react.go's WithStatePreHandler). The ReAct loop also
				// emits a Role==Tool MessageOutput for each tool's result —
				// filtering to Assistant only avoids double-counting that as
				// a second "turn". Previously StepsUsed was only bumped
				// inside runToolPipeline — i.e. only on a successfully-
				// dispatched tool call — so any turn that didn't reach that
				// pipeline (a malformed tool-call arguments string, a
				// remember/update_plan/ask_operator call, ...) consumed a
				// real Eino iteration invisibly. Live-observed: steps_used
				// showed 9 while Eino had already exhausted its real
				// 15-iteration budget and failed the task — an operator
				// watching "9/15" had no way to see the real ceiling
				// approaching. This is now the single, accurate choke point;
				// runToolPipeline's own increment is removed to avoid
				// double-counting a turn that also ran a tool.
				if r.store != nil && event.Output.MessageOutput.Role == schema.Assistant {
					_ = r.store.Update(task.ID, func(t *agent.Task) { t.StepsUsed++ })
				}
			}
		}

		if runCtx.Err() != nil {
			return nil // Stop() already transitioned the task to StatusStopped
		}

		// A run-time error (e.g. adk.ErrExceedMaxIterations, or a mid-run model
		// failure) must fail the task — previously this was silently dropped and
		// the task completed anyway with a stale/empty summary, mirroring what
		// graph.go's own budget-exhaustion path does explicitly instead.
		//
		// One class is worth a bounded retry rather than an immediate fail:
		// live-observed against Ollama-served gpt-oss:20b, a tool call's own
		// argument JSON can come back truncated mid-generation ("error parsing
		// tool call: ... unexpected end of JSON input") — a transient
		// generation hiccup on ONE turn, not a structural problem with the
		// goal or the model's reasoning (re-running the identical setup
		// immediately afterward succeeded cleanly). Failing the whole task for
		// one bad turn wastes everything already accomplished; this mirrors
		// graph.go's own LLM-call retry discipline (retryNode) for the
		// backend that, until now, had none at all for this error class.
		if runErr != nil {
			if isTransientToolCallError(runErr) && transientRetries < maxTransientRetries {
				transientRetries++
				openingMessage = buildRetryAfterErrorMessage(t.Goal, trace)
				r.store.RecordActivity(agent.ActivityEvent{
					SessionID: t.SessionID, TaskID: task.ID, Kind: agent.ActPlan,
					Summary: fmt.Sprintf("transient tool-call error, retrying (%d/%d): %s",
						transientRetries, maxTransientRetries, runErr.Error()),
				})
				continue
			}
			_ = r.store.Transition(task.ID, agent.StatusFailed, func(t *agent.Task) { t.LastError = runErr.Error() })
			return runErr
		}

		// ask_operator pauses the task for operator input, mirroring graph.go's
		// askNode exactly (StatusPaused + Question set) — checked ahead of
		// finish since ReturnDirectly guarantees at most one of the two fires
		// per run.
		if question, asked := ask.get(); asked {
			question = agent.SanitizeAgentText(question)
			if question == "" {
				question = "The agent needs operator input to continue."
			}
			if err := r.store.Transition(task.ID, agent.StatusPaused, func(t *agent.Task) { t.Question = question }); err != nil {
				return err
			}
			r.store.RecordActivity(agent.ActivityEvent{SessionID: t.SessionID, TaskID: task.ID, Kind: agent.ActAsk, Summary: question})
			return nil
		}

		var called bool
		summary, findings, called = capture.get()
		if !called && lastMessage != nil {
			// The model's finish intent can still be a complete, well-formed
			// {"summary":...,"findings":...} object even when this turn's
			// response wasn't routed into a proper tool_calls entry — live-
			// observed with gpt-oss-20b's Harmony channel format via LM
			// Studio, which sometimes serializes the tool call as inline
			// text ("...json{\"summary\":...}") instead. Recovering that
			// embedded object gives the real, clean answer instead of the
			// channel-routing prose wrapped around it; falls back to the raw
			// content when nothing finishArgs-shaped is found.
			if s, f, ok := tryParseEmbeddedFinishJSON(lastMessage.Content); ok {
				summary, findings = s, f
			} else {
				summary = lastMessage.Content
			}
		}
		// The graph backend sanitizes every model-authored string before it
		// becomes wire-visible on Task (agent.SanitizeAgentText's own doc
		// comment: "must be the only line of defense"); this backend never
		// did. Belt-and-suspenders even after the recovery above: strips any
		// remaining raw "<|...|>" control token the embedded-JSON parse
		// didn't catch (e.g. a genuinely non-JSON fallback).
		summary = agent.SanitizeAgentText(summary)
		findings = agent.SanitizeFindings(findings)
		// believesFinished covers BOTH ways this loop iteration can end with
		// nothing left queued: the model properly invoked the finish tool
		// (called), or it simply stopped requesting any tool at all and the
		// ADK loop's iterator exhausted with a plain assistant message
		// (lastMessage != nil, called false) — live-observed with
		// gpt-oss-20b, which sometimes ends a turn this way instead of
		// calling finish. The original guard only checked `called`, so a
		// model that silently stops instead of calling finish sailed straight
		// past the premature-completion check with an under-covered goal —
		// arguably worse than a model that at least tries to call finish
		// early, since nothing here signals "I think I'm done" for the guard
		// to catch. Both cases mean the same thing operationally: this run
		// produced its last action, decide now whether that was premature.
		believesFinished := called || lastMessage != nil
		if believesFinished && nudgeCount < maxNudges && len(enumParts) >= 2 && r.cfg.PlanLLM != nil {
			if uncovered := auditFinishCoverage(ctx, r.cfg.PlanLLM, enumParts, summary, trace); len(uncovered) > 0 {
				nudgeCount++
				openingMessage = buildIncompleteGoalsMessage(t.Goal, uncovered, trace)
				r.store.RecordActivity(agent.ActivityEvent{
					SessionID: t.SessionID, TaskID: task.ID, Kind: agent.ActPlan,
					Summary: fmt.Sprintf("finish reached with %d/%d goal parts uncovered — continuing (%d/%d nudges)",
						len(uncovered), len(enumParts), nudgeCount, maxNudges),
				})
				continue
			}
		}
		break
	}
	// Fallback for real evidence with nothing to show it: found via live
	// long-form testing — a task can complete every part of a multi-part
	// goal (confirmed via the execution records) yet still call finish with
	// an empty summary, even with the instruction above telling the model to
	// call finish alone. That instruction reduces the failure but a 14B
	// model won't always comply, and a completed task the operator can't see
	// any result from is a worse experience than an obviously auto-generated
	// one. Never fabricates: every line is a verbatim (compressed) result
	// the task actually produced.
	if strings.TrimSpace(summary) == "" {
		if pairs := trace.pairs(); len(pairs) > 0 {
			var b strings.Builder
			b.WriteString("(auto-generated: the model completed the task but did not provide a summary)\n")
			for _, p := range pairs {
				fmt.Fprintf(&b, "- %s: %s\n", p.Tool, strings.TrimSpace(agent.CompressObservation(p.Result)))
			}
			summary = strings.TrimSpace(b.String())
		}
	}
	if err := r.store.Transition(task.ID, agent.StatusCompleted, func(t *agent.Task) {
		t.Summary = summary
		for i := range t.Plan {
			if t.Plan[i].Status == agent.StepPending || t.Plan[i].Status == agent.StepRunning {
				t.Plan[i].Status = agent.StepDone
				t.Plan[i].Result = summary
			}
		}
		if len(findings) > 0 {
			t.Findings = findings
		}
	}); err != nil {
		return err
	}

	// Auto-skill-creation: fire-and-forget, mirrors graph.go's
	// proposeSkillFromTrajectory exactly (same non-blocking curator worker,
	// same worked-tactic filter).
	if r.cfg.SkillCurator != nil {
		observations, toolsUsed := trace.snapshot()
		traj := skillcurator.Trajectory{
			TaskID: task.ID, SessionID: t.SessionID, Goal: t.Goal, Summary: summary, Observations: observations,
		}
		if r.cfg.Knowledge != nil {
			for _, tl := range toolsUsed {
				for _, tac := range r.cfg.Knowledge.ListTactics(tl, 3) {
					if tac.Outcome == "worked" {
						traj.Tactics = append(traj.Tactics, tac)
					}
				}
			}
		}
		r.cfg.SkillCurator(context.WithoutCancel(ctx), traj)
	}

	return nil
}

func (r *EinoRunner) instruction(goal string) string {
	// identity/principles come from the same agent.yaml document the graph
	// backend's StepSystemPrompt renders — see Config.Persona's doc comment.
	// A zero-value Persona (tests that don't set it) falls back to a minimal
	// identity line rather than an empty prefix.
	identity := strings.TrimSpace(r.cfg.Persona.Identity)
	if identity == "" {
		identity = "You are Yummi, an autonomous penetration-testing operator assistant."
	}
	instr := identity + "\n"
	if principles := strings.TrimSpace(r.cfg.Persona.Principles); principles != "" {
		instr += "\nREASONING PRINCIPLES:\n" + principles + "\n"
	}
	// Found via live long-form testing: a model reproducibly hallucinated a
	// tool restriction that doesn't exist ("the provided functions do not
	// support running standard system commands like ps") for an ordinary
	// process-listing request, then reported the task complete anyway having
	// run nothing at all. The prior wording here ("use run_command only for
	// things that aren't pentest tools at all") reads as a narrow allowlist
	// when it was only ever meant as a preference ordering — leading with
	// run_command's actual scope (unrestricted, always available) instead of
	// leaving it as the last, exception-shaped option removes that gap.
	instr += "\nYour current goal: " + goal + "\n\n" +
		"run_command executes ANY shell command in the sandbox and is always available for it — this " +
		"includes ordinary system utilities (ps, free, df, cat, ls, find, awk, netstat, uptime, and " +
		"everything else) that have no dedicated tool. It is never restricted to a specific list; when in " +
		"doubt, just call it directly rather than concluding an action isn't possible. Prefer a dedicated " +
		"tool only when one exists for that exact operation: nmap, whatweb, curl, gobuster, ffuf, and nikto " +
		"each have one (nmap_scan, whatweb_scan, curl_probe, gobuster_dir, ffuf_fuzz, nikto_scan) — use " +
		"those instead of run_command for those six, since they give you a real schema instead of a " +
		"free-text string. "
	if r.cfg.MCPList != nil {
		instr += "For any other cataloged sandbox pentest tool (amass, hydra, sqlmap, wpscan, and more), " +
			"call search_pentest_tools first to see what's available and its exact argument schema, then " +
			"call run_pentest_tool with the tool name and matching args — but this is only relevant for " +
			"tools actually in that catalog; use run_command directly for everything else. "
	}
	instr += "Use remember to persist durable facts. Use update_plan if your approach changes. Call finish with " +
		"a concrete, factual summary once the goal is achieved — cite exact ports/versions/paths from tool " +
		"output, never vague claims. Call ask_operator only when genuinely blocked. " +
		// Found via live long-form testing: finish was observed completing tasks
		// with an EMPTY summary despite real evidence having been gathered in
		// the same turn. Root cause: finish's arguments are drafted at
		// generation time — if it's called in the same turn as another tool,
		// it is written before that tool has actually run, so there is
		// nothing real yet to summarize (an empty summary here is the
		// anti-fabrication instinct working correctly, just at the wrong
		// moment — the fix is timing, not honesty).
		"IMPORTANT: never call finish in the same turn as another tool. Call finish alone, in its own " +
		"turn, only after you have already seen a prior tool's actual result — that is the only way its " +
		"summary can cite real evidence instead of being written blind."
	if len(r.cfg.SubAgents) > 0 {
		instr += " Use handoff to delegate a focused subtask to a specialist — call it more than once in the same " +
			"turn to delegate to several specialists at once, they run concurrently."
	}
	return instr
}

// leadingIntRe extracts the first integer in an audit-reply line ("3", "3)",
// "- 3", "part 3", ...) — auditFinishCoverage's only parsing need.
var leadingIntRe = regexp.MustCompile(`\d{1,2}`)

// auditFinishCoverage asks a narrow, single-purpose LLM call whether a
// finish attempt actually addressed every part of a multi-part goal, and
// returns the text of any part it did not. This replaces a previous
// heuristic that compared total tool-call count against part count — which
// stopped firing the moment a task made more tool calls than it had parts,
// near-universal in any real multi-step task since real-world friction means
// a single part often needs several calls on its own. Live-observed: an
// 8-part goal reached FINISH at 58 real tool calls with one entire
// requested part (a hydra credential check) never attempted at all, and the
// old comparison (58 >= 8) never caught it because it measured volume, not
// coverage. Judging whether a summary/trace covers a goal is the same class
// of narrower, easier task AuditGoalParts already leans on being more
// reliable than the main loop at (see that function's own doc comment) —
// the same principle applied at the other end of the task instead of the
// start. Best-effort: any LLM error, empty reply, or an "ALL COVERED" answer
// returns nil so an audit-call failure never blocks a genuine finish.
func auditFinishCoverage(ctx context.Context, llm agent.LLM, parts []string, summary string, trace *runTrace) []string {
	if llm == nil || len(parts) < 2 {
		return nil
	}
	var goalParts strings.Builder
	for i, p := range parts {
		fmt.Fprintf(&goalParts, "%d) %s\n", i+1, strings.TrimSpace(p))
	}
	var work strings.Builder
	if pairs := trace.pairs(); len(pairs) > 0 {
		for _, p := range pairs {
			fmt.Fprintf(&work, "- %s: %s\n", p.Tool, strings.TrimSpace(agent.CompressObservation(p.Result)))
		}
	} else {
		work.WriteString("(no tool calls recorded)\n")
	}
	system := "You audit a penetration-testing agent's finished work against the original goal. You do not plan or execute anything."
	user := fmt.Sprintf(
		"GOAL PARTS:\n%s\nWORK ACTUALLY DONE (tool calls and their results):\n%s\nFINISH SUMMARY:\n%s\n\n"+
			"List the number of each goal part above that was NOT genuinely attempted by the work done, one number per line. "+
			"A part counts as attempted if a real command targeting it was run and produced a result, even a failure or an empty "+
			"result — only flag a part with NO corresponding attempt at all. If every part was attempted, reply with exactly: ALL COVERED",
		goalParts.String(), work.String(), strings.TrimSpace(summary))
	raw, err := llm.Chat(ctx, system, user)
	if err != nil {
		return nil
	}
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.Contains(strings.ToUpper(raw), "ALL COVERED") {
		return nil
	}
	var uncovered []string
	seen := make(map[int]bool, len(parts))
	for line := range strings.SplitSeq(raw, "\n") {
		m := leadingIntRe.FindString(line)
		if m == "" {
			continue
		}
		n, convErr := strconv.Atoi(m)
		if convErr != nil || n < 1 || n > len(parts) || seen[n] {
			continue
		}
		seen[n] = true
		uncovered = append(uncovered, parts[n-1])
	}
	return uncovered
}

// buildIncompleteGoalsMessage is the opening message for the single bounded
// re-run the premature-FINISH guard triggers. Like the paused-resume message,
// it starts a fresh conversation, so it must carry both the uncovered parts
// and the work already done — otherwise the model would redo covered parts
// instead of advancing to the untouched ones. It never fabricates: the
// "already done" lines are verbatim (compressed) results the task actually
// produced.
func buildIncompleteGoalsMessage(goal string, uncoveredParts []string, trace *runTrace) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Continuing the original multi-part goal: %s\n\n", goal)
	b.WriteString("You called finish, but these parts were NOT genuinely attempted:\n")
	for i, p := range uncoveredParts {
		fmt.Fprintf(&b, "  %d) %s\n", i+1, strings.TrimSpace(p))
	}
	if pairs := trace.pairs(); len(pairs) > 0 {
		b.WriteString("\nWork already completed (do NOT repeat these):\n")
		for _, p := range pairs {
			fmt.Fprintf(&b, "  - %s: %s\n", p.Tool, strings.TrimSpace(agent.CompressObservation(p.Result)))
		}
	}
	b.WriteString("\nRun a real command/tool for each part listed above, then call finish again once every part is genuinely done.")
	return b.String()
}

// tryParseEmbeddedFinishJSON looks for a {"summary":...} object embedded
// anywhere in raw fallback content and parses it as finishArgs. ok is false
// when no valid finishArgs-shaped object is found (no braces at all, invalid
// JSON, or a valid-but-empty summary) — the caller keeps the raw content
// as-is in that case rather than risk silently swallowing a real answer.
func tryParseEmbeddedFinishJSON(content string) (summary string, findings []string, ok bool) {
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start < 0 || end < start {
		return "", nil, false
	}
	var args finishArgs
	if err := json.Unmarshal([]byte(content[start:end+1]), &args); err != nil {
		return "", nil, false
	}
	if strings.TrimSpace(args.Summary) == "" {
		return "", nil, false
	}
	return args.Summary, args.Findings, true
}

// maxTransientRetries bounds how many times a transient tool-call error (see
// isTransientToolCallError) is retried before the task is failed for real —
// one, so a genuinely broken setup (not just a one-off generation hiccup)
// doesn't retry forever.
const maxTransientRetries = 1

// isTransientToolCallError reports whether err is the specific class of
// failure observed live against Ollama-served gpt-oss:20b: the model's tool
// call arguments came back truncated mid-generation, and Ollama's own
// tool-call parser hard-fails the request (HTTP 500) instead of tolerating
// it. This is conservative on purpose — it matches only the exact message
// text this failure produces, not "any error", so a genuine model/server
// failure (network down, OOM, ...) still fails the task immediately rather
// than being silently retried into a longer hang.
func isTransientToolCallError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "error parsing tool call")
}

// buildRetryAfterErrorMessage is the opening message for the bounded retry
// isTransientToolCallError triggers. Like buildIncompleteGoalsMessage, it
// starts a fresh conversation, so it must carry forward whatever work
// already completed — otherwise the retry would redo it. Generic (not
// enumerated-parts-aware) since a transient error can interrupt a
// single-intent goal just as easily as a multi-part one.
func buildRetryAfterErrorMessage(goal string, trace *runTrace) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Continuing toward the original goal: %s\n\n", goal)
	b.WriteString("The previous attempt was interrupted by a transient tool-call error, not a real problem " +
		"with the goal or your reasoning — just retry.\n")
	if pairs := trace.pairs(); len(pairs) > 0 {
		b.WriteString("\nWork already completed (do NOT repeat these):\n")
		for _, p := range pairs {
			fmt.Fprintf(&b, "  - %s: %s\n", p.Tool, strings.TrimSpace(agent.CompressObservation(p.Result)))
		}
	}
	b.WriteString("\nContinue from here toward the goal.")
	return b.String()
}

// ── agent.TaskRunner ─────────────────────────────────────────────────────

// CreateAndPlan creates the task and immediately marks it planned: unlike
// the graph engine, this backend has no separate up-front planning LLM
// call — the ChatModelAgent plans and acts in one native tool-calling loop
// once Run() starts. StatusPlanning → StatusPlanned is a valid transition
// (see agent.validNext), so the task is immediately startable.
func (r *EinoRunner) CreateAndPlan(ctx context.Context, sessionID, goal string) (agent.Task, error) {
	task, err := r.store.CreateSubtask(sessionID, goal, r.cfg.StepBudget, "")
	if err != nil {
		return agent.Task{}, err
	}
	if err := r.store.Transition(task.ID, agent.StatusPlanned, func(t *agent.Task) {
		// The native loop does not need an up-front LLM plan, but Store startup
		// recovery deliberately refuses active tasks with an empty durable plan.
		// This anchor makes an interrupted Eino run reconstructively resumable.
		t.Plan = []agent.PlanStep{{
			ID: "native-loop", Description: "Continue the native tool-calling workflow and verify prior side effects", Status: agent.StepPending,
		}}
	}); err != nil {
		return agent.Task{}, err
	}
	task, _ = r.store.Get(task.ID)
	return task, nil
}

func (r *EinoRunner) Start(taskID string, operatorMessage string) error {
	task, ok := r.store.Get(taskID)
	if !ok {
		return fmt.Errorf("%w: %s", agent.ErrTaskNotFound, taskID)
	}
	if task.Status != agent.StatusPlanned && task.Status != agent.StatusPaused {
		return fmt.Errorf("task %s is %s; can only start planned or paused tasks", taskID, task.Status)
	}
	if strings.TrimSpace(operatorMessage) != "" {
		r.mu.Lock()
		r.resumeMsg[taskID] = operatorMessage
		r.mu.Unlock()
	}
	go func() {
		if err := r.Run(context.Background(), &task); err != nil {
			_ = r.store.Transition(taskID, agent.StatusFailed, func(t *agent.Task) { t.LastError = err.Error() })
		}
	}()
	return nil
}

func (r *EinoRunner) Stop(taskID string) error {
	r.mu.Lock()
	cancel := r.active[taskID]
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	r.gate.Clear(taskID)
	task, ok := r.store.Get(taskID)
	if !ok {
		return fmt.Errorf("%w: %s", agent.ErrTaskNotFound, taskID)
	}
	if task.Status.Terminal() {
		return nil
	}
	return r.store.Transition(taskID, agent.StatusStopped, func(t *agent.Task) {
		t.PendingProposalID = ""
		t.PendingCommand = ""
		t.PendingRisk = ""
	})
}

// Approve resolves a pending gate request, checking this runner's own gate
// first and then every configured sub-agent's — mirroring graph.Runner's own
// Approve exactly (found via live E2E: a delegated sub-agent runs its own
// task loop against its own private *Gate, so the only API path,
// HandleApproveTask -> the top-level TaskRunner.Approve, would otherwise
// never reach it and every sub-agent RUN_COMMAND above low risk would hang
// until it timed out).
func (r *EinoRunner) Approve(taskID, proposalID string, approved bool, reason string) error {
	if err := r.gate.Resolve(taskID, proposalID, approved, reason); err == nil {
		return nil
	}
	for _, sub := range r.cfg.SubAgents {
		if err := sub.Gate().Resolve(taskID, proposalID, approved, reason); err == nil {
			return nil
		}
	}
	return fmt.Errorf("no pending approval for task %s proposal %s", taskID, proposalID)
}

func (r *EinoRunner) ApprovalMode() agent.ApprovalMode {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cfg.ApprovalMode
}

func (r *EinoRunner) SetApprovalMode(mode agent.ApprovalMode) error {
	if mode != agent.ModeAskPermission && mode != agent.ModeAuto {
		return fmt.Errorf("invalid approval mode: %q", mode)
	}
	r.mu.Lock()
	r.cfg.ApprovalMode = mode
	subs := r.cfg.SubAgents
	r.mu.Unlock()
	for _, sub := range subs {
		_ = sub.SetApprovalMode(mode)
	}
	return nil
}

// SetTaskAutoApprove flips one task's AutoApproveNonCritical override. One
// store.Update call suffices regardless of whether taskID names this
// runner's own top-level task or a delegated sub-agent's (SubAgents are
// *agent.Runner graph instances, per this package's documented design) —
// every Runner in the whole hierarchy shares the SAME *agent.Store instance
// (wired identically in cmd/orchestrator/main.go).
func (r *EinoRunner) SetTaskAutoApprove(taskID string, enable bool) error {
	return r.store.Update(taskID, func(t *agent.Task) { t.AutoApproveNonCritical = enable })
}

// ResumeRecovered reconstructs a fresh native conversation from durable task
// state. It cannot restore Eino's private token history, so the recovery
// message explicitly requires side-effect verification before repetition.
func (r *EinoRunner) ResumeRecovered() (int, error) {
	if r == nil {
		return 0, nil
	}
	started := 0
	for _, task := range r.store.Recoverable() {
		if err := r.Start(task.ID, "The orchestrator restarted. Reconstruct the workflow from the durable goal and plan; verify state before repeating side effects."); err != nil {
			return started, err
		}
		started++
	}
	return started, nil
}

func (r *EinoRunner) Gate() *agent.Gate { return r.gate }

// Roles returns the sorted sub-agent role names available for delegation
// (empty when none are configured), mirroring graph.Runner.Roles.
func (r *EinoRunner) Roles() []string {
	hs := r.Handoffs()
	roles := make([]string, 0, len(hs))
	for role := range hs {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	return roles
}

// DelegationRuns exposes the bounded specialist lifecycle history. Nil-safe:
// (*agent.DelegationCoordinator).List tolerates a nil receiver.
func (r *EinoRunner) DelegationRuns() []agent.DelegationRun { return r.cfg.Delegations.List() }

func (r *EinoRunner) InterruptDelegation(id string) error {
	if r.cfg.Delegations == nil {
		return fmt.Errorf("this backend has no sub-agents configured")
	}
	return r.cfg.Delegations.Interrupt(id)
}

func (r *EinoRunner) Capabilities() agent.BackendCapabilities {
	if r == nil {
		return agent.BackendCapabilities{Backend: "eino", ExecutionModel: "native_tool_loop", RecoveryModel: "reconstructive_checkpoint"}
	}
	r.mu.Lock()
	cfg := r.cfg
	r.mu.Unlock()
	skillCount := 0
	if cfg.Skills != nil {
		skillCount = len(cfg.Skills.All())
	}
	retrieval := "disabled"
	if cfg.Retrieval != nil {
		retrieval = "configured"
	}
	return agent.BackendCapabilities{
		Backend: "eino", ExecutionModel: "native_tool_loop", RecoveryModel: "reconstructive_checkpoint",
		Resumable: true, NativeToolCalling: true, ApprovalMode: cfg.ApprovalMode,
		Roles: r.Roles(), MaxHandoffDepth: 1, MaxConcurrentSubAgents: 3,
		MemoryEnabled: cfg.Knowledge != nil && cfg.Retrieval != nil, RetrievalModel: retrieval,
		SkillsEnabled: cfg.Skills != nil, SkillCount: skillCount,
		SkillGovernance: "versioned_threat_scanned_operator_approved",
	}
}
