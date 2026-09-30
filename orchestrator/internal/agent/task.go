// Package agent implements the Yummi agentic task loop: plan-then-execute +
// ReAct over the existing conductor (LLM+validator) and execution pipeline.
// Task state lives here, in Go structs — never inside the model's history.
package agent

import (
	"slices"
	"time"
)

// TaskStatus is the lifecycle state of an agentic task.
type TaskStatus string

const (
	// StatusPlanning means the plan LLM call is in flight.
	StatusPlanning TaskStatus = "planning"
	// StatusPlanned means a plan exists and the task awaits operator start.
	StatusPlanned TaskStatus = "planned"
	// StatusRunning means the step loop is active.
	StatusRunning TaskStatus = "running"
	// StatusAwaitingApproval means the loop is blocked on a medium+ risk gate.
	StatusAwaitingApproval TaskStatus = "awaiting_approval"
	// StatusRetryingLLM means the graph is in exponential backoff after an LLM error.
	StatusRetryingLLM TaskStatus = "retrying_llm"
	// StatusSummarizing means the graph is compressing observation history.
	StatusSummarizing TaskStatus = "summarizing"
	// StatusPaused means the loop stopped and waits for the operator
	// (ask_operator action, stuck detection, LLM failure, approval timeout).
	StatusPaused TaskStatus = "paused"
	// StatusCompleted means the model emitted finish (or budget forced one).
	StatusCompleted TaskStatus = "completed"
	// StatusFailed means an unrecoverable error ended the task.
	StatusFailed TaskStatus = "failed"
	// StatusStopped means the operator stopped the task.
	StatusStopped TaskStatus = "stopped"
	// StatusInterrupted means the orchestrator restarted while the task was active.
	StatusInterrupted TaskStatus = "interrupted"
)

// Terminal reports whether the status is final.
func (s TaskStatus) Terminal() bool {
	switch s {
	case StatusCompleted, StatusFailed, StatusStopped, StatusInterrupted:
		return true
	default:
		return false
	}
}

// validNext maps each status to its allowed successors.
var validNext = map[TaskStatus][]TaskStatus{
	StatusPlanning:         {StatusPlanned, StatusFailed, StatusStopped},
	StatusPlanned:          {StatusRunning, StatusStopped},
	StatusRunning:          {StatusAwaitingApproval, StatusRetryingLLM, StatusSummarizing, StatusPaused, StatusCompleted, StatusFailed, StatusStopped},
	StatusAwaitingApproval: {StatusRunning, StatusPaused, StatusStopped},
	StatusRetryingLLM:      {StatusRunning, StatusPaused, StatusFailed, StatusStopped},
	StatusSummarizing:      {StatusRunning, StatusStopped},
	StatusPaused:           {StatusRunning, StatusStopped},
}

// ValidTransition reports whether from → to is a legal lifecycle move.
func ValidTransition(from, to TaskStatus) bool {
	return slices.Contains(validNext[from], to)
}

// StepStatus is the lifecycle state of a single plan step.
type StepStatus string

const (
	StepPending StepStatus = "pending"
	StepRunning StepStatus = "running"
	StepDone    StepStatus = "done"
	StepFailed  StepStatus = "failed"
	StepSkipped StepStatus = "skipped"
)

// PlanStep is one step of the task plan, produced and updated by the model.
type PlanStep struct {
	ID          string        `json:"id"`
	Description string        `json:"description"`
	Status      StepStatus    `json:"status"`
	Result      string        `json:"result,omitempty"`
	Duration    time.Duration `json:"duration,omitempty"`
	RetryCount  int           `json:"retry_count,omitempty"`
	Error       string        `json:"error,omitempty"`
	// Command is the actual shell command dispatched for this step (set by
	// markStepStatus at completion time, whether it succeeded or failed) — the
	// operator-facing plan view previously only showed the model's own
	// natural-language step description, with no record of what was literally
	// run. Also lets finishNode cross-check the final report against which
	// tools' commands actually completed vs. failed/aborted.
	Command string `json:"command,omitempty"`
}

// Checkpoint records the graph position after each node transition for crash recovery.
type Checkpoint struct {
	TaskID              string       `json:"task_id"`
	CurrentNode         string       `json:"current_node"`
	StepIndex           int          `json:"step_index"`
	RetryCtx            RetryContext `json:"retry_ctx"`
	TokensUsed          int          `json:"tokens_used"`
	Summary             string       `json:"summary,omitempty"`
	LastObservation     string       `json:"last_observation,omitempty"`
	RecentCommands      []string     `json:"recent_commands,omitempty"`
	ExecutedCommands    []string     `json:"executed_commands,omitempty"`
	MemoryObservations  []string     `json:"memory_observations,omitempty"`
	ConsecutiveFailures int          `json:"consecutive_failures,omitempty"`
	ResumeAfterRestart  bool         `json:"resume_after_restart,omitempty"`
	SavedAt             int64        `json:"saved_at"`
}

// Task is the persistent state of one agentic task.
type Task struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	// ParentTaskID is set when this task is a sub-agent run delegated via
	// [HANDOFF] from another task (see agent.go's Handoffs/RunSubtask). Empty
	// for a top-level orchestrator task. Lets the UI surface a sub-task's own
	// pending approval under the parent it was spawned from, instead of it
	// being invisible to the operator (it has its own task ID the chat/web UI
	// never otherwise learns about).
	ParentTaskID string     `json:"parent_task_id,omitempty"`
	Goal         string     `json:"goal"`
	Status       TaskStatus `json:"status"`
	Plan         []PlanStep `json:"plan,omitempty"`
	StepsUsed    int        `json:"steps_used"`
	StepBudget   int        `json:"step_budget"`
	Summary      string     `json:"summary,omitempty"`
	Findings     []string   `json:"findings,omitempty"`
	// FindingRecords is the structured, verdict-bearing counterpart to
	// Findings — see verifyFindingDetail in verdict.go. Additive: only
	// FindingsDetail entries (title/evidence/step_id) produce a record, so
	// this may be shorter than Findings. Wire-visible on purpose (title,
	// evidence, and reason are all already sanitized/server-authored text).
	FindingRecords []FindingRecord `json:"finding_records,omitempty"`
	Question       string          `json:"question,omitempty"` // set when paused via ask_operator
	LastError      string          `json:"last_error,omitempty"`
	TokensUsed     int             `json:"tokens_used,omitempty"`

	// Checkpoint holds the last saved graph position (crash recovery).
	Checkpoint Checkpoint `json:"checkpoint,omitempty"`

	// ObservationHistory holds full tool outputs for audit (not persisted —
	// runtime only). Currently unpopulated by any node, but json:"-" (not
	// omitempty) deliberately, matching ActivityEvent.Detail's pattern: this
	// is target-controlled command output, which must never reach a wire
	// client — it exists for server-side diagnostics, if and when something
	// starts writing to it, not for UI display.
	ObservationHistory []string `json:"-"`

	// Pending* describe the command the task is blocked on while
	// awaiting_approval. They are ephemeral (not persisted) and exist so the
	// task.status ws event is self-sufficient for the UI's approve action.
	PendingProposalID string `json:"pending_proposal_id,omitempty"`
	PendingCommand    string `json:"pending_command,omitempty"`
	PendingRisk       string `json:"pending_risk,omitempty"`

	// AutoApproveNonCritical is an operator-set, per-task override: when
	// true, a proposal whose risk is anything other than high/critical
	// (conductor.IsHighSeverity) skips the ask_permission gate and runs
	// immediately, the same way auto mode would — but "high"/"critical" and
	// anything destructiveFloor forces (ForceApprovalReason) still always
	// gate, in EITHER mode. This exists because the runner-wide ApprovalMode
	// is deliberately NOT something a running task can change (SetApprovalMode's
	// own doc comment: "tasks already mid-loop keep the mode they were built
	// with") — an operator watching a long recon task who has seen enough
	// low/medium-risk proposals to trust the pattern had no way to stop
	// clicking approve for the rest of THIS task without either tolerating
	// every future task (SetApprovalMode is global) or missing a genuinely
	// dangerous step (auto mode has no risk floor at all beyond
	// ForceApprovalReason). Ephemeral like the Pending* fields above — not a
	// persist() column — it only needs to survive for the running process's
	// lifetime, not across a restart.
	AutoApproveNonCritical bool `json:"auto_approve_non_critical,omitempty"`

	CreatedAt int64 `json:"created_at"`
	UpdatedAt int64 `json:"updated_at"`
}
