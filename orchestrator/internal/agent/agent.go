package agent

import "context"

// Agent is the interface satisfied by all autonomous task runners.
// Runner implements Agent for single-agent use. Future orchestrators
// implement it by delegating to sub-agents via Handoffs.
type Agent interface {
	// Name returns a stable identifier for this agent type.
	Name() string
	// Handoffs returns the sub-agents this agent can delegate to, keyed by role name.
	Handoffs() map[string]Agent
	// Run executes a task synchronously, returning when the task reaches a terminal state.
	Run(ctx context.Context, task *Task) error
}
