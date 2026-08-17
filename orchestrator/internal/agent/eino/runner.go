package eino

import (
	"context"
	"fmt"
	"sort"
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
	ModelBaseURL string // e.g. "http://localhost:11434/v1"
	ModelName    string // e.g. "qwen3:14b"
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

	cm, err := NewOllamaChatModel(ctx, r.cfg.ModelBaseURL, r.cfg.ModelName)
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

	einoAgent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "yummi",
		Description: "an autonomous penetration-testing operator assistant",
		Instruction: instr,
		Model:       cm, // already model.ToolCallingChatModel per NewOllamaChatModel's return type
		// StepBudget's real enforcement: without this, Eino's own default
		// (20) governs regardless of what the task was actually configured
		// for.
		MaxIterations: r.cfg.StepBudget,
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
			ToolsNodeConfig: compose.ToolsNodeConfig{Tools: tools, ExecuteSequentially: true},
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
	iter := runner.Run(runCtx, []*schema.Message{schema.UserMessage(initialGoal)})

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
		}
	}

	if runCtx.Err() != nil {
		return nil // Stop() already transitioned the task to StatusStopped
	}

	// A run-time error (e.g. adk.ErrExceedMaxIterations, or a mid-run model
	// failure) must fail the task — previously this was silently dropped and
	// the task completed anyway with a stale/empty summary, mirroring what
	// graph.go's own budget-exhaustion path does explicitly instead.
	if runErr != nil {
		_ = r.store.Transition(task.ID, agent.StatusFailed, func(t *agent.Task) { t.LastError = runErr.Error() })
		return runErr
	}

	// ask_operator pauses the task for operator input, mirroring graph.go's
	// askNode exactly (StatusPaused + Question set) — checked ahead of
	// finish since ReturnDirectly guarantees at most one of the two fires
	// per run.
	if question, asked := ask.get(); asked {
		if question == "" {
			question = "The agent needs operator input to continue."
		}
		if err := r.store.Transition(task.ID, agent.StatusPaused, func(t *agent.Task) { t.Question = question }); err != nil {
			return err
		}
		r.store.RecordActivity(agent.ActivityEvent{SessionID: t.SessionID, TaskID: task.ID, Kind: agent.ActAsk, Summary: question})
		return nil
	}

	summary, findings, called := capture.get()
	if !called && lastMessage != nil {
		summary = lastMessage.Content
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
	instr += "\nYour current goal: " + goal + "\n\n" +
		"For nmap, whatweb, curl, gobuster, ffuf, or nikto, prefer the dedicated tool " +
		"(nmap_scan, whatweb_scan, curl_probe, gobuster_dir, ffuf_fuzz, nikto_scan) over run_command — " +
		"it gives you a real schema instead of a free-text string. "
	if r.cfg.MCPList != nil {
		instr += "For any other sandbox pentest tool (amass, hydra, sqlmap, wpscan, and more), call " +
			"search_pentest_tools first to see what's available and its exact argument schema, then call " +
			"run_pentest_tool with the tool name and matching args. Use run_command only for things that aren't " +
			"pentest tools at all (git, find, awk, ...). "
	} else {
		instr += "Use run_command for everything else. "
	}
	instr += "Use remember to persist durable facts. Use update_plan if your approach changes. Call finish with " +
		"a concrete, factual summary once the goal is achieved — cite exact ports/versions/paths from tool " +
		"output, never vague claims. Call ask_operator only when genuinely blocked."
	if len(r.cfg.SubAgents) > 0 {
		instr += " Use handoff to delegate a focused subtask to a specialist — call it more than once in the same " +
			"turn to delegate to several specialists at once, they run concurrently."
	}
	return instr
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
