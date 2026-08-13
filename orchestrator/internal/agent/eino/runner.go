package eino

import (
	"context"
	"fmt"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"github.com/n0connect/yummi/orchestrator/internal/agent"
	"github.com/n0connect/yummi/orchestrator/internal/conductor"
	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
)

// Config mirrors agent.Config's shape for the fields this backend actually
// uses. Sub-agent delegation, retry tuning, and skill/memory prompt
// injection are intentionally out of scope for this plan — StepBudget,
// ApprovalMode, and Knowledge cover the v1 feature set.
type Config struct {
	StepBudget   int
	ApprovalMode agent.ApprovalMode
	ModelBaseURL string // e.g. "http://localhost:11434/v1"
	ModelName    string // e.g. "qwen3:14b"
	Knowledge    *knowledge.Store
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
	return &EinoRunner{
		store: store, validator: validator, exec: exec,
		gate: agent.NewGate(), cfg: cfg,
		active: make(map[string]context.CancelFunc),
	}
}

// ── agent.Agent ──────────────────────────────────────────────────────────

func (r *EinoRunner) Name() string { return "yummi-eino" }

// Handoffs: no sub-agent delegation in this plan (follow-up work).
func (r *EinoRunner) Handoffs() map[string]agent.Agent { return nil }

func (r *EinoRunner) Run(ctx context.Context, task *agent.Task) error {
	t, ok := r.store.Get(task.ID)
	if !ok {
		return fmt.Errorf("%w: %s", agent.ErrTaskNotFound, task.ID)
	}
	if t.Status != agent.StatusPlanned && t.Status != agent.StatusPaused {
		return fmt.Errorf("task %s is %s; expected planned or paused", task.ID, t.Status)
	}
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

	rcDeps := runCommandDeps{
		taskID: task.ID, sessionID: t.SessionID, store: r.store, exec: r.exec, gate: r.gate,
		validator: r.validator, approvalMode: func() agent.ApprovalMode { return r.cfg.ApprovalMode },
	}
	tools := []tool.BaseTool{
		newRunCommandTool(rcDeps),
		newRememberTool(rememberDeps{taskID: task.ID, sessionID: t.SessionID, knowledge: r.cfg.Knowledge, origin: "agent"}),
		newAskOperatorTool(),
		newFinishTool(),
		newUpdatePlanTool(r.store, task.ID),
	}

	einoAgent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "yummi",
		Description: "an autonomous penetration-testing operator assistant",
		Instruction: r.instruction(t.Goal),
		Model:       cm, // already model.ToolCallingChatModel per NewOllamaChatModel's return type
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{Tools: tools},
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

	runner := adk.NewRunner(runCtx, adk.RunnerConfig{Agent: einoAgent})
	iter := runner.Run(runCtx, []*schema.Message{schema.UserMessage(t.Goal)})

	var lastMessage *schema.Message
	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event.Output != nil && event.Output.MessageOutput != nil && !event.Output.MessageOutput.IsStreaming {
			lastMessage = event.Output.MessageOutput.Message
		}
	}

	if runCtx.Err() != nil {
		return nil // Stop() already transitioned the task to StatusStopped
	}

	summary := ""
	if lastMessage != nil {
		summary = lastMessage.Content
	}
	return r.store.Transition(task.ID, agent.StatusCompleted, func(t *agent.Task) {
		t.Summary = summary
	})
}

func (r *EinoRunner) instruction(goal string) string {
	return "You are Yummi, an autonomous penetration-testing operator assistant. " +
		"Your current goal: " + goal + "\n\n" +
		"Use run_command to execute commands in the audited sandbox. Use remember to persist durable " +
		"facts. Use update_plan if your approach changes. Call finish with a concrete, factual summary " +
		"once the goal is achieved — cite exact ports/versions/paths from tool output, never vague claims. " +
		"Call ask_operator only when genuinely blocked."
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
	if err := r.store.Transition(task.ID, agent.StatusPlanned, nil); err != nil {
		return agent.Task{}, err
	}
	task, _ = r.store.Get(task.ID)
	return task, nil
}

func (r *EinoRunner) Start(taskID string, _ string) error {
	task, ok := r.store.Get(taskID)
	if !ok {
		return fmt.Errorf("%w: %s", agent.ErrTaskNotFound, taskID)
	}
	if task.Status != agent.StatusPlanned && task.Status != agent.StatusPaused {
		return fmt.Errorf("task %s is %s; can only start planned or paused tasks", taskID, task.Status)
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
	return r.store.Transition(taskID, agent.StatusStopped, nil)
}

func (r *EinoRunner) Approve(taskID, proposalID string, approved bool, reason string) error {
	return r.gate.Resolve(taskID, proposalID, approved, reason)
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
	r.mu.Unlock()
	return nil
}

// ResumeRecovered: this backend does not implement checkpoint restoration
// (follow-up work) — recovered tasks stay StatusInterrupted, matching what
// Store.recoverInterrupted() already does uniformly at startup.
func (r *EinoRunner) ResumeRecovered() (int, error) { return 0, nil }

func (r *EinoRunner) Gate() *agent.Gate { return r.gate }

// Roles: no sub-agents in this plan.
func (r *EinoRunner) Roles() []string { return nil }

// DelegationRuns: no sub-agents in this plan.
func (r *EinoRunner) DelegationRuns() []agent.DelegationRun { return []agent.DelegationRun{} }

func (r *EinoRunner) InterruptDelegation(_ string) error {
	return fmt.Errorf("this backend does not support sub-agent delegation")
}
