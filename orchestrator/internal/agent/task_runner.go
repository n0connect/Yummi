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
}
