package agent

// ActivityKind classifies one legible agent activity event — the units the UI
// renders as a timeline so the operator can see what the agent is doing.
type ActivityKind string

const (
	ActPlan        ActivityKind = "plan"
	ActCommand     ActivityKind = "command"
	ActCommandDone ActivityKind = "command_result"
	ActMemory      ActivityKind = "memory_recalled"
	ActSkill       ActivityKind = "skill_applied"
	ActHandoffIn   ActivityKind = "handoff_started"
	ActHandoffOut  ActivityKind = "handoff_completed"
	ActFinding     ActivityKind = "finding"
	ActContext     ActivityKind = "context_saved"
	ActAsk         ActivityKind = "ask"
	ActFinish      ActivityKind = "finish"
)

// ActivityEvent is one observational, legible step in the agent's work.
// ParentTaskID is set on sub-agent events so the UI can nest them under the
// orchestrator's handoff span. It carries no execution authority — purely
// observational, emitted alongside (never instead of) the real exec path.
type ActivityEvent struct {
	ID           string       `json:"id"`
	SessionID    string       `json:"session_id"`
	TaskID       string       `json:"task_id"`
	ParentTaskID string       `json:"parent_task_id,omitempty"`
	Kind         ActivityKind `json:"kind"`
	Summary      string       `json:"summary"`
	// Detail is an internal diagnostic payload. It can contain model rationale,
	// recalled memory, or compressed target-controlled command output, so it is
	// persisted for server-side diagnostics but never serialized to UI clients.
	Detail    string `json:"-"`
	Timestamp int64  `json:"timestamp"` // unix millis
}

// ActivityEnvelope wraps an event for pub/sub (mirrors Event for tasks).
type ActivityEnvelope struct {
	Type  string        `json:"type"` // always "agent.activity"
	Event ActivityEvent `json:"data"`
}
