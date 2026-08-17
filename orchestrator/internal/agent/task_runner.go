package agent

import "context"

// TaskRunner is the full surface internal/api needs from an agentic backend —
// a superset of Agent (Name/Handoffs/Run) covering task lifecycle, approval,
// and delegation management. *Runner (the graph engine) and any future
// backend (e.g. the Eino-based one in internal/agent/eino) both implement
// this, so internal/api never depends on which backend is wired at
// cmd/orchestrator/main.go's build-time YUMMI_AGENT_BACKEND selection.
type TaskRunner interface {
	Agent

	// CreateAndPlan creates a top-level task and runs the planning LLM call
	// synchronously.
	CreateAndPlan(ctx context.Context, sessionID, goal string) (Task, error)
	// Start launches the step loop for a planned or paused task asynchronously.
	// operatorMessage is optional operator input for a task paused via ask_operator.
	Start(taskID string, operatorMessage string) error
	// Stop cancels a task's loop and marks it stopped.
	Stop(taskID string) error
	// Approve resolves the pending gate verdict for a task (or one of its
	// sub-agents').
	Approve(taskID, proposalID string, approved bool, reason string) error
	// ApprovalMode returns the mode new task states are built with.
	ApprovalMode() ApprovalMode
	// SetApprovalMode changes the mode applied to tasks started from this
	// point on, propagating to configured sub-agents.
	SetApprovalMode(mode ApprovalMode) error
	// ResumeRecovered starts top-level tasks the store's startup recovery
	// converted to a safe checkpoint. Returns the count started.
	ResumeRecovered() (int, error)
	// Gate exposes the approval gate for the API layer's pending-approval read.
	Gate() *Gate
	// Roles returns the sorted sub-agent role names available for delegation
	// (empty when the backend has none configured).
	Roles() []string
	// DelegationRuns exposes the bounded specialist lifecycle history for the
	// TRACE view.
	DelegationRuns() []DelegationRun
	// InterruptDelegation cancels one addressable specialist without
	// stopping its parent or siblings.
	InterruptDelegation(id string) error
	// Capabilities is the truthful, operator-visible contract for the selected
	// backend. It prevents UI/docs from inferring parity from a backend name.
	Capabilities() BackendCapabilities
}

type BackendCapabilities struct {
	Backend                string       `json:"backend"`
	ExecutionModel         string       `json:"execution_model"`
	RecoveryModel          string       `json:"recovery_model"`
	Resumable              bool         `json:"resumable"`
	NativeToolCalling      bool         `json:"native_tool_calling"`
	ApprovalMode           ApprovalMode `json:"approval_mode"`
	Roles                  []string     `json:"roles"`
	MaxHandoffDepth        int          `json:"max_handoff_depth"`
	MaxConcurrentSubAgents int          `json:"max_concurrent_sub_agents"`
	MemoryEnabled          bool         `json:"memory_enabled"`
	RetrievalModel         string       `json:"retrieval_model"`
	SkillsEnabled          bool         `json:"skills_enabled"`
	SkillCount             int          `json:"skill_count"`
	SkillGovernance        string       `json:"skill_governance"`
}
