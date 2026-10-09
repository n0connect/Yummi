package agent

// ActivityKind classifies one legible agent activity event — the units the UI
// renders as a timeline so the operator can see what the agent is doing.
type ActivityKind string

const (
	ActPlan        ActivityKind = "plan"
	ActCommand     ActivityKind = "command"
	ActCommandDone ActivityKind = "command_result"
	ActMemory      ActivityKind = "memory_recalled"
	// ActSkill means the model itself loaded a playbook — graph's SKILL_VIEW
	// node or Eino's skill_view tool. This is the trustworthy "the model is
	// actually using skills" signal: an operator or evaluator can trust it
	// because nothing else ever emits it.
	ActSkill ActivityKind = "skill_applied"
	// ActSkillSuggested means the SYSTEM pushed a goal-matched playbook into
	// the task's opening context before the model took any action — the
	// Eino backend's one-time "most relevant playbook" push (see
	// eino/brief.go's buildPlaybookBlock). It is deliberately a different
	// kind from ActSkill: a suggestion the model received is not the same
	// claim as the model choosing to load a skill, and conflating the two
	// made it impossible to tell, from TRACE or the activity feed, whether
	// skill_view was ever actually called by a connected model.
	ActSkillSuggested ActivityKind = "skill_suggested"
	ActHandoffIn      ActivityKind = "handoff_started"
	ActHandoffOut     ActivityKind = "handoff_completed"
	ActFinding        ActivityKind = "finding"
	ActContext        ActivityKind = "context_saved"
	// ActContextRejected means a REMEMBER write (save or forget) was blocked
	// by the content-layer threat scan before it ever reached the store.
	// Comparative review (2026-10, vs. hermes-agent's memory_tool.py, which
	// never silently drops a flagged write — it keeps the raw entry visible
	// to the operator with the block reason) found Yummi's own rejection path
	// told only the MODEL, via the returned observation — an operator
	// watching TRACE had no way to see a REMEMBER was ever blocked at all.
	// This kind closes that gap. The summary is always threatscan's own
	// pattern-name diagnostic (see FirstThreatMessage), never the rejected
	// content itself.
	ActContextRejected ActivityKind = "context_rejected"
	ActAsk             ActivityKind = "ask"
	ActFinish          ActivityKind = "finish"
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
