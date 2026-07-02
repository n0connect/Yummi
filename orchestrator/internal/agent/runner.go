package agent

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/n0connect/yummi/orchestrator/internal/conductor"
	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
	"github.com/n0connect/yummi/orchestrator/internal/skill"
	"github.com/n0connect/yummi/orchestrator/internal/skillcurator"
)

// LLM is the minimal model interface the runner needs.
// *llm.Client satisfies it.
type LLM interface {
	Chat(ctx context.Context, system, user string) (string, error)
}

// Execer starts a supervisable command execution.
// A non-nil error from Start/Run is an infrastructure failure; a
// ToolResult.IsError=true means the command ran but failed and the agent loop
// should continue.
type Execer interface {
	// Start enqueues a command and returns a handle to supervise it.
	Start(ctx context.Context, sessionID, command string) (ExecHandle, error)
	// Run is a convenience helper: Start + a single bounded wait.
	Run(ctx context.Context, sessionID, command string) (ToolResult, error)
}

// ExecHandle supervises one running command.
type ExecHandle interface {
	// Wait blocks up to soft; returns (result, true) when the command reaches a
	// terminal state, or (zero, false) if soft elapsed while it is still running.
	Wait(soft time.Duration) (ToolResult, bool)
	// EgressHold reports whether the command is currently held awaiting operator
	// egress approval (gateway Ask mode), and the target it's blocked on. This
	// lets the loop distinguish "command is slow" from "command is blocked on
	// the operator" after a non-done Wait, so it surfaces an accurate prompt
	// instead of a misleading "still running".
	EgressHold() (target string, held bool)
	// Cancel requests cancellation of the running command.
	Cancel()
}

// ApprovalMode controls when the agent pauses for operator approval. There are
// exactly two modes — no risk-based gating.
type ApprovalMode string

const (
	// ModeAskPermission gates EVERY command (regardless of content) on the operator.
	ModeAskPermission ApprovalMode = "ask_permission"
	// ModeAuto runs every command autonomously with no approval gate.
	ModeAuto ApprovalMode = "auto"
)

// Config tunes the runner.
type Config struct {
	StepBudget             int           // max loop iterations per task (default 15)
	ApprovalTimeout        time.Duration // how long to block on the gate (default 20m)
	TokenBudget            int           // rough token budget for memory compression (default 6000)
	SoftTimeout            time.Duration // per-Wait window before consulting the LLM (default 60s)
	SubAgentTimeout        time.Duration // hard ceiling on one HANDOFF sub-agent run (default 10m)
	MaxConsecutiveFailures int           // back-to-back failures before REPEATED_FAILURE escalation (default 3)
	ApprovalMode           ApprovalMode  // ask_permission (default) or auto
	Persona                AgentPersona  // document-driven model persona (loaded from agent.yaml)
	Retry                  RetryConfig   // LLM retry backoff (zero → defaultRetryConfig)

	// Knowledge is the world-model store the loop records findings to and reads
	// memory from. Nil disables knowledge recording/retrieval (degraded mode).
	Knowledge *knowledge.Store
	// Retrieval selects prior memory injected into the step prompt. Nil disables
	// memory injection. Typically knowledge.NewStructuredRetrieval(Knowledge).
	Retrieval knowledge.RetrievalStrategy

	// Skills is the declarative playbook registry; relevant skills are injected
	// into the plan and step prompts. Nil disables skill injection. A sub-agent
	// is typically given a Subset scoped to its role.
	Skills *skill.Registry

	// SubAgents maps a role name to a specialized sub-agent Runner the brain may
	// hand a focused subtask to via [HANDOFF]. Empty/nil disables delegation —
	// and sub-agents themselves are built with an empty map, capping handoff
	// depth at exactly 1 (no recursion).
	SubAgents map[string]*Runner

	// SkillCurator, if set, is invoked asynchronously (never blocking the
	// graph) after a task finishes successfully, to propose a candidate skill
	// drafted from its trajectory. Nil disables auto-skill-creation. Fired
	// from both top-level and sub-agent task completions (finishNode is
	// shared by both).
	SkillCurator func(ctx context.Context, traj skillcurator.Trajectory)
}

// Runner drives agentic task loops. One goroutine per running task.
// Runner implements the Agent interface for single-agent use.
type Runner struct {
	store     *Store
	llm       LLM
	validator *conductor.Validator
	exec      Execer
	gate      *Gate
	cfg       Config

	mu     sync.Mutex
	active map[string]context.CancelFunc
}

// NewRunner wires the loop driver. validator may be nil (no scope filtering —
// tests only; production always passes the engine's validator).
func NewRunner(store *Store, llm LLM, validator *conductor.Validator, exec Execer, cfg Config) *Runner {
	if cfg.StepBudget <= 0 {
		cfg.StepBudget = 15
	}
	if cfg.ApprovalTimeout <= 0 {
		cfg.ApprovalTimeout = 20 * time.Minute
	}
	if cfg.TokenBudget <= 0 {
		cfg.TokenBudget = 6000
	}
	if cfg.SoftTimeout <= 0 {
		cfg.SoftTimeout = 60 * time.Second
	}
	if cfg.SubAgentTimeout <= 0 {
		cfg.SubAgentTimeout = 10 * time.Minute
	}
	if cfg.MaxConsecutiveFailures <= 0 {
		cfg.MaxConsecutiveFailures = 3
	}
	if cfg.ApprovalMode == "" {
		cfg.ApprovalMode = ModeAskPermission
	}
	if cfg.Retry.MaxAttempts <= 0 {
		cfg.Retry = defaultRetryConfig
	}
	return &Runner{
		store:     store,
		llm:       llm,
		validator: validator,
		exec:      exec,
		gate:      NewGate(),
		cfg:       cfg,
		active:    make(map[string]context.CancelFunc),
	}
}

// ── Agent interface ──────────────────────────────────────────────────────────

// Name returns the agent's stable identifier.
func (r *Runner) Name() string { return "yummi" }

// Handoffs returns the configured sub-agents as the delegation set, or nil when
// none are configured (single-agent use, and every sub-agent).
func (r *Runner) Handoffs() map[string]Agent {
	if len(r.cfg.SubAgents) == 0 {
		return nil
	}
	out := make(map[string]Agent, len(r.cfg.SubAgents))
	for role, sub := range r.cfg.SubAgents {
		out[role] = sub
	}
	return out
}

// Roles returns the sorted sub-agent role names available for delegation
// (empty for a plain runner). Used by the read API / TRACE view.
func (r *Runner) Roles() []string {
	hs := r.Handoffs()
	roles := make([]string, 0, len(hs))
	for k := range hs {
		roles = append(roles, k)
	}
	sort.Strings(roles)
	return roles
}

// SubResult is what a sub-agent returns to the orchestrator after a handoff.
type SubResult struct {
	Summary  string
	Findings []string
}

// RunSubtask creates, plans, and synchronously runs a focused subtask on this
// runner, returning the resulting task's summary and findings. The handoff node
// uses it to delegate to a specialized sub-agent. The sub-agent shares the
// caller's Store and Knowledge, so its findings/tactics join the one world
// model automatically.
func (r *Runner) RunSubtask(ctx context.Context, sessionID, goal, parentTaskID string) (SubResult, error) {
	// parentTaskID is threaded straight into task creation (persisted on the
	// Task row itself) rather than mutated on the shared Runner config — the
	// latter raced under concurrent handoffs from different parent tasks on
	// the same Runner, since Config.ParentTaskID was one field shared across
	// every call. It also nests this sub-run's plan/command/finding activity
	// under the orchestrator's handoff span in the TRACE view.
	task, err := r.createAndPlan(ctx, sessionID, goal, parentTaskID)
	if err != nil {
		return SubResult{}, err
	}
	if err := r.Run(ctx, &task); err != nil {
		return SubResult{}, err
	}
	t, _ := r.store.Get(task.ID)
	return SubResult{Summary: t.Summary, Findings: t.Findings}, nil
}

// Run executes a task synchronously, blocking until completion.
// The task must already be in StatusPlanned or StatusPaused.
func (r *Runner) Run(ctx context.Context, task *Task) error {
	t, ok := r.store.Get(task.ID)
	if !ok {
		return fmt.Errorf("%w: %s", ErrTaskNotFound, task.ID)
	}
	if t.Status != StatusPlanned && t.Status != StatusPaused {
		return fmt.Errorf("task %s is %s; expected planned or paused", task.ID, t.Status)
	}
	if err := r.store.Transition(task.ID, StatusRunning, func(t *Task) { t.Question = "" }); err != nil {
		return err
	}
	s := r.buildState(task.ID)
	return Execute(ctx, s, nodeStep)
}

// ── Public API ───────────────────────────────────────────────────────────────

// Gate exposes the approval gate for the API layer.
func (r *Runner) Gate() *Gate { return r.gate }

// CreateAndPlan creates a top-level task and runs the planning LLM call
// synchronously. Callers that want async planning wrap this in a goroutine.
func (r *Runner) CreateAndPlan(ctx context.Context, sessionID, goal string) (Task, error) {
	return r.createAndPlan(ctx, sessionID, goal, "")
}

// createAndPlan is the shared implementation behind CreateAndPlan (top-level
// task, parentTaskID "") and RunSubtask (sub-agent task, parentTaskID set).
func (r *Runner) createAndPlan(ctx context.Context, sessionID, goal, parentTaskID string) (Task, error) {
	if r.llm == nil {
		return Task{}, fmt.Errorf("LLM not configured")
	}
	task, err := r.store.CreateSubtask(sessionID, goal, r.cfg.StepBudget, parentTaskID)
	if err != nil {
		return Task{}, err
	}

	var skills string
	if r.cfg.Skills != nil {
		skills = r.cfg.Skills.Render(r.cfg.Skills.Select(goal, nil, 3))
	}
	system, user := BuildPlanPrompt(r.cfg.Persona, skills, goal)
	raw, err := r.llm.Chat(ctx, system, user)
	if err == nil {
		if _, perr := ParsePlan(raw); perr != nil {
			// One retry with a format reminder.
			raw, err = r.llm.Chat(ctx, system, user+
				"\n\nIMPORTANT: Your previous response was not valid JSON. Reply ONLY with the JSON object.")
		}
	}
	if err != nil {
		_ = r.store.Transition(task.ID, StatusFailed, func(t *Task) { t.LastError = err.Error() })
		return Task{}, fmt.Errorf("plan LLM call: %w", err)
	}
	steps, perr := ParsePlan(raw)
	if perr != nil {
		_ = r.store.Transition(task.ID, StatusFailed, func(t *Task) { t.LastError = perr.Error() })
		return Task{}, perr
	}
	if err := r.store.Transition(task.ID, StatusPlanned, func(t *Task) { t.Plan = steps }); err != nil {
		return Task{}, err
	}
	task, _ = r.store.Get(task.ID)
	r.store.RecordActivity(ActivityEvent{
		SessionID: task.SessionID, TaskID: task.ID, ParentTaskID: task.ParentTaskID,
		Kind: ActPlan, Summary: fmt.Sprintf("%d steps planned", len(steps)),
	})
	return task, nil
}

// Start launches the step loop for a planned or paused task asynchronously.
func (r *Runner) Start(taskID string) error {
	task, ok := r.store.Get(taskID)
	if !ok {
		return fmt.Errorf("%w: %s", ErrTaskNotFound, taskID)
	}
	if task.Status != StatusPlanned && task.Status != StatusPaused {
		return fmt.Errorf("task %s is %s; can only start planned or paused tasks", taskID, task.Status)
	}
	if err := r.store.Transition(taskID, StatusRunning, func(t *Task) { t.Question = "" }); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	r.active[taskID] = cancel
	r.mu.Unlock()

	go func() {
		defer func() {
			cancel()
			r.mu.Lock()
			delete(r.active, taskID)
			r.mu.Unlock()
		}()
		r.loop(ctx, taskID)
	}()
	return nil
}

// Stop cancels a task's loop and marks it stopped.
func (r *Runner) Stop(taskID string) error {
	r.mu.Lock()
	cancel := r.active[taskID]
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	r.gate.Clear(taskID)
	task, ok := r.store.Get(taskID)
	if !ok {
		return fmt.Errorf("%w: %s", ErrTaskNotFound, taskID)
	}
	if task.Status.Terminal() {
		return nil
	}
	return r.store.Transition(taskID, StatusStopped, nil)
}

// Approve resolves the pending gate verdict for a task. Every sub-agent runs
// its own task loop against its own private *Gate (see Config.SubAgents), so
// a delegated sub-task's pending approval never lives on the top-level
// runner's gate. We first try our own gate (the common case — the
// orchestrator's own task), then fall back to each configured sub-agent's
// gate (handoff depth is capped at 1, so this set is small and bounded — an
// explicit, known list of owners, not a guess). Task state itself
// (PendingCommand/PendingRisk) is already visible regardless of which gate
// holds it, since runners share one Store; only verdict delivery was
// previously unreachable for sub-agent tasks.
func (r *Runner) Approve(taskID, proposalID string, approved bool, reason string) error {
	if err := r.gate.Resolve(taskID, proposalID, approved, reason); err == nil {
		return nil
	}
	for _, sub := range r.cfg.SubAgents {
		if err := sub.gate.Resolve(taskID, proposalID, approved, reason); err == nil {
			return nil
		}
	}
	return fmt.Errorf("no pending approval for task %s proposal %s", taskID, proposalID)
}

// ApprovalMode returns the mode new task states are built with.
func (r *Runner) ApprovalMode() ApprovalMode {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cfg.ApprovalMode
}

// SetApprovalMode changes the mode applied to tasks started from this point
// on; tasks already mid-loop keep the mode they were built with (cfg is
// copied by value into GraphState in buildState).
func (r *Runner) SetApprovalMode(mode ApprovalMode) error {
	if mode != ModeAskPermission && mode != ModeAuto {
		return fmt.Errorf("invalid approval mode: %q", mode)
	}
	r.mu.Lock()
	r.cfg.ApprovalMode = mode
	r.mu.Unlock()
	return nil
}

// ── Internal ─────────────────────────────────────────────────────────────────

// buildState constructs GraphState for a task, restoring from checkpoint if one exists.
func (r *Runner) buildState(taskID string) *GraphState {
	task, _ := r.store.Get(taskID)
	s := &GraphState{
		taskID:       taskID,
		store:        r.store,
		llm:          r.llm,
		validator:    r.validator,
		exec:         r.exec,
		gate:         r.gate,
		cfg:          r.cfg,
		parentTaskID: task.ParentTaskID,
		memCtx:       MemoryContext{TokenBudget: r.cfg.TokenBudget},
	}
	// Restore from checkpoint on crash-recovery resume.
	if task.Checkpoint.CurrentNode != "" {
		s.retryCtx = task.Checkpoint.RetryCtx
		s.memCtx.TokensUsed = task.Checkpoint.TokensUsed
		s.memCtx.Summary = task.Checkpoint.Summary
	}
	return s
}

// loop is the goroutine entry point for async task execution via Start().
func (r *Runner) loop(ctx context.Context, taskID string) {
	s := r.buildState(taskID)

	// Resume from checkpoint if available; otherwise start at the step node.
	startNode := nodeStep
	task, _ := r.store.Get(taskID)
	if task.Checkpoint.CurrentNode != "" {
		startNode = task.Checkpoint.CurrentNode
	}

	if err := Execute(ctx, s, startNode); err != nil {
		if ctx.Err() != nil {
			return // stopped by operator; Stop() handles the status transition
		}
		slog.Error("agent: graph execution error", "task", taskID, "error", err)
		_ = r.store.Transition(taskID, StatusFailed, func(t *Task) {
			t.LastError = err.Error()
		})
	}
}
