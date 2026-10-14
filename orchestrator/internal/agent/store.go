package agent

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

var (
	// ErrTaskNotFound indicates there is no task for the provided id.
	ErrTaskNotFound = errors.New("task not found")
	// ErrInvalidTransition indicates a forbidden lifecycle move.
	ErrInvalidTransition = errors.New("invalid task transition")
)

// Event is published whenever a task lifecycle changes.
// Type is "task." + status, e.g. "task.running", "task.awaiting_approval".
type Event struct {
	Type      string `json:"type"`
	Timestamp int64  `json:"timestamp"`
	Task      Task   `json:"task"`
}

// Store keeps agent tasks with optional SQLite persistence.
type Store struct {
	mu           sync.RWMutex
	items        map[string]Task
	db           *sql.DB // nil = in-memory only
	subscribers  map[int]chan Event
	nextSubID    int
	activitySubs map[int]chan ActivityEnvelope
	nextActSubID int
}

const schema = `
CREATE TABLE IF NOT EXISTS agent_tasks (
	id TEXT PRIMARY KEY,
	session_id TEXT NOT NULL,
	goal TEXT NOT NULL,
	status TEXT NOT NULL,
	plan TEXT,
	steps_used INTEGER NOT NULL DEFAULT 0,
	step_budget INTEGER NOT NULL DEFAULT 15,
	summary TEXT,
	findings TEXT,
	question TEXT,
	last_error TEXT,
	checkpoint TEXT,
	tokens_used INTEGER NOT NULL DEFAULT 0,
	parent_task_id TEXT,
	finding_records TEXT,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);`

// NewStore creates a task store. db may be nil (in-memory only, for tests).
// On startup, active top-level tasks are converted to safe, resumable step
// checkpoints. Active child tasks are stopped so their parent can reconcile
// the interrupted handoff without double-running side effects.
func NewStore(db *sql.DB) (*Store, error) {
	s := &Store{
		items:        make(map[string]Task),
		db:           db,
		subscribers:  make(map[int]chan Event),
		activitySubs: make(map[int]chan ActivityEnvelope),
	}
	if db != nil {
		if _, err := db.Exec(schema); err != nil {
			return nil, fmt.Errorf("agent schema: %w", err)
		}
		// Migrate existing databases that pre-date the checkpoint/tokens_used columns.
		_, _ = db.Exec(`ALTER TABLE agent_tasks ADD COLUMN checkpoint TEXT`)
		_, _ = db.Exec(`ALTER TABLE agent_tasks ADD COLUMN tokens_used INTEGER NOT NULL DEFAULT 0`)
		_, _ = db.Exec(`ALTER TABLE agent_tasks ADD COLUMN parent_task_id TEXT`)
		_, _ = db.Exec(`ALTER TABLE agent_tasks ADD COLUMN finding_records TEXT`)
		_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_tasks_session ON agent_tasks(session_id)`)
		_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS agent_activity (
			id TEXT PRIMARY KEY, session_id TEXT NOT NULL, task_id TEXT NOT NULL,
			parent_task_id TEXT, kind TEXT NOT NULL, summary TEXT NOT NULL,
			detail TEXT, timestamp INTEGER NOT NULL)`)
		_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_activity_session ON agent_activity(session_id)`)
		if err := s.loadFromDB(); err != nil {
			return nil, fmt.Errorf("agent load: %w", err)
		}
		if err := s.recoverInterrupted(); err != nil {
			return nil, fmt.Errorf("agent recover interrupted tasks: %w", err)
		}
	}
	return s, nil
}

// ReplaceModelPlan accepts intent changes, never model-authored execution
// history. Recorded progress is immutable to the model, even if omitted or
// renamed in its replacement list. This does not judge goal completion.
func (s *Store) ReplaceModelPlan(id string, proposed []PlanStep) error {
	return s.Update(id, func(task *Task) {
		recorded := make(map[string]PlanStep, len(task.Plan))
		for _, step := range task.Plan {
			if step.Command != "" || step.Status == StepDone || step.Status == StepFailed || step.Status == StepRunning {
				recorded[step.ID] = step
			}
		}
		seen := make(map[string]bool, len(proposed))
		next := make([]PlanStep, 0, len(proposed)+len(recorded))
		for i, step := range proposed {
			if step.ID == "" {
				step.ID = fmt.Sprintf("s%d", i+1)
			}
			if seen[step.ID] {
				continue
			}
			seen[step.ID] = true
			if prior, ok := recorded[step.ID]; ok {
				next = append(next, prior)
				continue
			}
			status := StepPending
			// Skipping is a model decision, not a claim that execution happened.
			if step.Status == StepSkipped {
				status = StepSkipped
			}
			next = append(next, PlanStep{ID: step.ID, Description: step.Description, Status: status})
		}
		for _, prior := range task.Plan {
			if _, ok := recorded[prior.ID]; ok && !seen[prior.ID] {
				next = append(next, prior)
				seen[prior.ID] = true
			}
		}
		task.Plan = next
	})
}

// Create registers a new top-level task in the planning state.
func (s *Store) Create(sessionID, goal string, stepBudget int) (Task, error) {
	return s.create(sessionID, goal, stepBudget, "")
}

// CreateSubtask registers a new task delegated from parentTaskID (a
// sub-agent run via [HANDOFF] — see RunSubtask). Identical to Create except
// the resulting Task carries ParentTaskID, so the operator-facing API/WS
// surface can tie a sub-task's own pending approval back to the task it was
// spawned from instead of it being an orphaned, undiscoverable ID.
func (s *Store) CreateSubtask(sessionID, goal string, stepBudget int, parentTaskID string) (Task, error) {
	return s.create(sessionID, goal, stepBudget, parentTaskID)
}

func (s *Store) create(sessionID, goal string, stepBudget int, parentTaskID string) (Task, error) {
	if goal == "" {
		return Task{}, errors.New("goal is required")
	}
	if sessionID == "" {
		sessionID = "default"
	}
	if stepBudget <= 0 {
		stepBudget = 15
	}
	now := time.Now().Unix()
	task := Task{
		ID:           uuid.NewString(),
		SessionID:    sessionID,
		ParentTaskID: parentTaskID,
		Goal:         goal,
		Status:       StatusPlanning,
		StepBudget:   stepBudget,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.persist(task); err != nil {
		return Task{}, err
	}
	s.mu.Lock()
	s.items[task.ID] = task
	s.mu.Unlock()
	s.publish(task)
	return task, nil
}

// Get returns one task by id.
func (s *Store) Get(id string) (Task, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.items[id]
	return t, ok
}

// ListBySession returns tasks for one session, latest first.
func (s *Store) ListBySession(sessionID string, limit int) []Task {
	if limit <= 0 {
		limit = 50
	}
	s.mu.RLock()
	out := make([]Task, 0)
	for _, t := range s.items {
		if t.SessionID == sessionID {
			out = append(out, t)
		}
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// cloneTaskSlices replaces t's slice fields with independent backing arrays.
// Task is stored and handed out BY VALUE (Get/ListBySession/Event.Task), so a
// bare struct copy still shares the same backing array for Plan/Findings/
// ObservationHistory with whatever the map currently holds. Update/Transition
// mutators (markStepStatus in particular) write into a slice ELEMENT in place
// (t.Plan[i].Result = ...), which — without this clone — mutates that shared
// backing array while some other goroutine may be concurrently reading it
// through an earlier Get() snapshot, entirely outside this store's lock. That
// is a genuine data race (found via live stress-testing: hammering GET
// /api/v1/tasks/{id} during an active run produced intermittent invalid JSON,
// a torn string read). Cloning here, BEFORE mutate runs, guarantees every
// mutation touches a backing array nobody else has ever seen.
func cloneTaskSlices(t *Task) {
	t.Plan = append([]PlanStep(nil), t.Plan...)
	t.Findings = append([]string(nil), t.Findings...)
	t.FindingRecords = append([]FindingRecord(nil), t.FindingRecords...)
	t.ObservationHistory = append([]string(nil), t.ObservationHistory...)
	t.Checkpoint.RecentCommands = append([]string(nil), t.Checkpoint.RecentCommands...)
	t.Checkpoint.ExecutedCommands = append([]string(nil), t.Checkpoint.ExecutedCommands...)
	t.Checkpoint.MemoryObservations = append([]string(nil), t.Checkpoint.MemoryObservations...)
	t.PendingGuidance = append([]string(nil), t.PendingGuidance...)
}

// Transition moves a task to a new status, applying mutate (may be nil) under
// the lock before validation-persisting. Returns ErrInvalidTransition for
// illegal moves.
func (s *Store) Transition(id string, to TaskStatus, mutate func(*Task)) error {
	s.mu.Lock()
	t, ok := s.items[id]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrTaskNotFound, id)
	}
	if !ValidTransition(t.Status, to) {
		s.mu.Unlock()
		return fmt.Errorf("%w: %s → %s", ErrInvalidTransition, t.Status, to)
	}
	cloneTaskSlices(&t)
	if mutate != nil {
		mutate(&t)
	}
	t.Status = to
	t.UpdatedAt = time.Now().Unix()
	if err := s.persist(t); err != nil {
		s.mu.Unlock()
		return err
	}
	s.items[id] = t
	s.mu.Unlock()
	s.publish(t)
	return nil
}

// Update mutates task fields without a status change (e.g. step progress).
func (s *Store) Update(id string, mutate func(*Task)) error {
	s.mu.Lock()
	t, ok := s.items[id]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrTaskNotFound, id)
	}
	cloneTaskSlices(&t)
	mutate(&t)
	t.UpdatedAt = time.Now().Unix()
	if err := s.persist(t); err != nil {
		s.mu.Unlock()
		return err
	}
	s.items[id] = t
	s.mu.Unlock()
	s.publish(t)
	return nil
}

// AppendGuidance queues an operator chat message for delivery into a
// running task's own context — see Task.PendingGuidance's doc comment.
func (s *Store) AppendGuidance(id, message string) error {
	return s.Update(id, func(t *Task) {
		t.PendingGuidance = append(t.PendingGuidance, message)
	})
}

// DrainGuidance returns and clears all queued guidance for a task. Both
// backends call this at a tool-call boundary (graph's node_tool.go, Eino's
// runToolPipeline) so queued messages reach the model on its very next turn
// — never silently lost, never delivered twice.
func (s *Store) DrainGuidance(id string) []string {
	s.mu.Lock()
	t, ok := s.items[id]
	if !ok || len(t.PendingGuidance) == 0 {
		s.mu.Unlock()
		return nil
	}
	drained := t.PendingGuidance
	t.PendingGuidance = nil
	t.UpdatedAt = time.Now().Unix()
	if err := s.persist(t); err != nil {
		// Non-fatal: worst case the same guidance is drained and delivered
		// again next call, which is a harmless duplicate instruction, not a
		// lost one. Keep the in-memory drain so the model still sees it now.
		s.items[id] = t
		s.mu.Unlock()
		s.publish(t)
		return drained
	}
	s.items[id] = t
	s.mu.Unlock()
	s.publish(t)
	return drained
}

// Subscribe registers a listener for task events (same pattern as execution.Store).
func (s *Store) Subscribe(buffer int) (int, <-chan Event, error) {
	if buffer <= 0 {
		buffer = 32
	}
	ch := make(chan Event, buffer)
	s.mu.Lock()
	id := s.nextSubID
	s.nextSubID++
	s.subscribers[id] = ch
	s.mu.Unlock()
	return id, ch, nil
}

// Unsubscribe removes a listener. The channel is intentionally not closed
// (publish snapshots sinks outside the lock; see execution.Store).
func (s *Store) Unsubscribe(id int) {
	s.mu.Lock()
	delete(s.subscribers, id)
	s.mu.Unlock()
}

func (s *Store) publish(t Task) {
	evt := Event{Type: "task." + string(t.Status), Timestamp: t.UpdatedAt, Task: t}
	s.mu.RLock()
	sinks := make([]chan Event, 0, len(s.subscribers))
	for _, ch := range s.subscribers {
		sinks = append(sinks, ch)
	}
	s.mu.RUnlock()
	for _, ch := range sinks {
		select {
		case ch <- evt:
		default: // drop on slow consumer; state is queryable via REST
		}
	}
}

func (s *Store) recoverInterrupted() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, t := range s.items {
		if !t.Status.Terminal() && t.Status != StatusPlanned && t.Status != StatusPaused {
			if len(t.Plan) == 0 {
				t.Status = StatusFailed
				t.LastError = "orchestrator restarted before planning completed; create a new task"
			} else if t.ParentTaskID != "" {
				t.Status = StatusStopped
				t.LastError = "sub-agent interrupted by orchestrator restart; parent task will reconcile"
			} else {
				t.Status = StatusPaused
				t.LastError = "orchestrator restarted; task recovered at a safe step boundary"
				t.Checkpoint.CurrentNode = nodeStep
				t.Checkpoint.ResumeAfterRestart = true
				t.Checkpoint.LastObservation = Wrap("TASK_RECOVERED",
					"The orchestrator restarted. An in-flight action may not have completed; verify state before repeating side effects.")
			}
			t.PendingProposalID, t.PendingCommand, t.PendingRisk = "", "", ""
			t.UpdatedAt = time.Now().Unix()
			if err := s.persist(t); err != nil {
				return err
			}
			s.items[id] = t
		}
	}
	return nil
}

// Recoverable returns top-level tasks that startup recovery made safe to
// resume. The returned slice is detached from store-owned backing arrays.
func (s *Store) Recoverable() []Task {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Task, 0)
	for _, task := range s.items {
		if task.Status == StatusPaused && task.Checkpoint.ResumeAfterRestart && task.ParentTaskID == "" {
			cloneTaskSlices(&task)
			out = append(out, task)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out
}

func (s *Store) persist(t Task) error {
	if s.db == nil {
		return nil
	}
	plan, _ := json.Marshal(t.Plan)
	findings, _ := json.Marshal(t.Findings)
	findingRecords, _ := json.Marshal(t.FindingRecords)
	checkpoint, _ := json.Marshal(t.Checkpoint)
	_, err := s.db.Exec(`
		INSERT INTO agent_tasks(id,session_id,goal,status,plan,steps_used,step_budget,summary,findings,question,last_error,checkpoint,tokens_used,parent_task_id,finding_records,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			status=excluded.status, plan=excluded.plan, steps_used=excluded.steps_used,
			summary=excluded.summary, findings=excluded.findings, question=excluded.question,
			last_error=excluded.last_error, checkpoint=excluded.checkpoint,
			tokens_used=excluded.tokens_used, finding_records=excluded.finding_records, updated_at=excluded.updated_at`,
		t.ID, t.SessionID, t.Goal, string(t.Status), string(plan), t.StepsUsed,
		t.StepBudget, t.Summary, string(findings), t.Question, t.LastError,
		string(checkpoint), t.TokensUsed, nullActStr(t.ParentTaskID), string(findingRecords), t.CreatedAt, t.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("persist task %s: %w", t.ID, err)
	}
	return nil
}

// RecordActivity persists (best-effort) and publishes one activity event.
// A nil/failed persist is logged, never fatal — observability must not break
// the loop. ID and Timestamp are filled when empty.
func (s *Store) RecordActivity(ev ActivityEvent) {
	if ev.ID == "" {
		ev.ID = uuid.NewString()
	}
	if ev.Timestamp == 0 {
		ev.Timestamp = time.Now().UnixMilli()
	}
	if s.db != nil {
		if _, err := s.db.Exec(
			`INSERT INTO agent_activity(id,session_id,task_id,parent_task_id,kind,summary,detail,timestamp)
			 VALUES(?,?,?,?,?,?,?,?)`,
			ev.ID, ev.SessionID, ev.TaskID, nullActStr(ev.ParentTaskID), string(ev.Kind), ev.Summary, ev.Detail, ev.Timestamp); err != nil {
			slog.Warn("agent: activity persist failed", "error", err)
		}
	}
	env := ActivityEnvelope{Type: "agent.activity", Event: ev}
	s.mu.RLock()
	sinks := make([]chan ActivityEnvelope, 0, len(s.activitySubs))
	for _, ch := range s.activitySubs {
		sinks = append(sinks, ch)
	}
	s.mu.RUnlock()
	for _, ch := range sinks {
		select {
		case ch <- env:
		default: // drop on slow consumer; history is queryable via ListActivity
		}
	}
}

// ListActivity returns a session's activity oldest-first (chronological trace).
func (s *Store) ListActivity(sessionID string, limit int) []ActivityEvent {
	if limit <= 0 {
		limit = 200
	}
	if s.db == nil {
		return []ActivityEvent{}
	}
	rows, err := s.db.Query(
		`SELECT id,session_id,task_id,COALESCE(parent_task_id,''),kind,summary,COALESCE(detail,''),timestamp
		 FROM agent_activity WHERE session_id=? ORDER BY timestamp ASC LIMIT ?`, sessionID, limit)
	if err != nil {
		slog.Error("agent: list activity", "error", err)
		return []ActivityEvent{}
	}
	defer func() { _ = rows.Close() }()
	out := make([]ActivityEvent, 0)
	for rows.Next() {
		var e ActivityEvent
		var kind string
		if err := rows.Scan(&e.ID, &e.SessionID, &e.TaskID, &e.ParentTaskID, &kind, &e.Summary, &e.Detail, &e.Timestamp); err != nil {
			continue
		}
		e.Kind = ActivityKind(kind)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		slog.Error("agent: list activity row iteration", "error", err)
	}
	return out
}

// ListTaskCommandEvidence returns one task's command and command-result
// activity, oldest-first. Scoped by task_id in the query itself, NOT by
// filtering ListActivity's session-wide window afterwards: a long multi-task
// session would otherwise push an earlier task's evidence out of the window,
// and a resumed run would silently lose the work it already did.
func (s *Store) ListTaskCommandEvidence(taskID string, limit int) []ActivityEvent {
	if limit <= 0 {
		limit = 2000
	}
	if s.db == nil {
		return []ActivityEvent{}
	}
	rows, err := s.db.Query(
		`SELECT id,session_id,task_id,COALESCE(parent_task_id,''),kind,summary,COALESCE(detail,''),timestamp
		 FROM agent_activity WHERE task_id=? AND kind IN (?, ?) ORDER BY timestamp ASC LIMIT ?`,
		taskID, string(ActCommand), string(ActCommandDone), limit)
	if err != nil {
		slog.Error("agent: list task command evidence", "error", err)
		return []ActivityEvent{}
	}
	defer func() { _ = rows.Close() }()
	out := make([]ActivityEvent, 0)
	for rows.Next() {
		var e ActivityEvent
		var kind string
		if err := rows.Scan(&e.ID, &e.SessionID, &e.TaskID, &e.ParentTaskID, &kind, &e.Summary, &e.Detail, &e.Timestamp); err != nil {
			continue
		}
		e.Kind = ActivityKind(kind)
		out = append(out, e)
	}
	return out
}

// SkillUsage summarizes how often a playbook was suggested by the system vs
// actually loaded by the model, across every session this orchestrator has
// run — the operator-visible evidence for deciding whether a skill earns its
// place in the catalogue.
//
// This is deliberately read-only visibility, not automatic pruning.
// Comparative review (2026-10) against another agent's periodic
// skill-library curator — which can archive/consolidate skills on its own
// schedule — found that pattern incompatible with Yummi's own invariant that
// nothing reaches or leaves the live skill registry without an explicit
// operator decision (the same reason skillcurator's drafts sit in a pending
// queue instead of auto-promoting). SkillUsageStats gives an operator the
// SAME visibility a periodic curator would act on, so they can retire a
// skill themselves — it does not act on their behalf.
type SkillUsage struct {
	Name           string `json:"name"`
	SuggestedCount int    `json:"suggested_count"` // ActSkillSuggested — the system pushed it, not evidence of model behavior
	AppliedCount   int    `json:"applied_count"`   // ActSkill — the model itself loaded it
	LastUsedAt     int64  `json:"last_used_at,omitempty"`
}

// SkillUsageStats aggregates every ActSkill/ActSkillSuggested activity ever
// recorded, grouped by skill name (the Summary field both kinds share — see
// ActSkill's doc comment), ranked by total usage descending. Returns an empty
// slice (never an error) for a nil-db store, matching ListActivity's own
// degrade-gracefully convention.
func (s *Store) SkillUsageStats() []SkillUsage {
	if s.db == nil {
		return []SkillUsage{}
	}
	rows, err := s.db.Query(
		`SELECT summary,
		        SUM(CASE WHEN kind = ? THEN 1 ELSE 0 END),
		        SUM(CASE WHEN kind = ? THEN 1 ELSE 0 END),
		        MAX(timestamp)
		 FROM agent_activity WHERE kind IN (?, ?)
		 GROUP BY summary ORDER BY 2 + 3 DESC, summary ASC`,
		string(ActSkillSuggested), string(ActSkill), string(ActSkillSuggested), string(ActSkill))
	if err != nil {
		slog.Error("agent: skill usage stats", "error", err)
		return []SkillUsage{}
	}
	defer func() { _ = rows.Close() }()
	out := make([]SkillUsage, 0)
	for rows.Next() {
		var u SkillUsage
		if err := rows.Scan(&u.Name, &u.SuggestedCount, &u.AppliedCount, &u.LastUsedAt); err != nil {
			continue
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		slog.Error("agent: skill usage stats row iteration", "error", err)
	}
	return out
}

// SubscribeActivity registers a listener for activity events (mirrors Subscribe).
func (s *Store) SubscribeActivity(buffer int) (int, <-chan ActivityEnvelope, error) {
	if buffer <= 0 {
		buffer = 64
	}
	ch := make(chan ActivityEnvelope, buffer)
	s.mu.Lock()
	id := s.nextActSubID
	s.nextActSubID++
	s.activitySubs[id] = ch
	s.mu.Unlock()
	return id, ch, nil
}

// UnsubscribeActivity removes an activity listener.
func (s *Store) UnsubscribeActivity(id int) {
	s.mu.Lock()
	delete(s.activitySubs, id)
	s.mu.Unlock()
}

func nullActStr(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func (s *Store) loadFromDB() error {
	rows, err := s.db.Query(`
		SELECT id,session_id,goal,status,plan,steps_used,step_budget,summary,findings,question,last_error,checkpoint,tokens_used,parent_task_id,finding_records,created_at,updated_at
		FROM agent_tasks ORDER BY created_at ASC`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	s.mu.Lock()
	defer s.mu.Unlock()
	for rows.Next() {
		var t Task
		var status string
		var plan, findings, summary, question, lastErr, checkpoint, parentTaskID, findingRecords sql.NullString
		if err := rows.Scan(&t.ID, &t.SessionID, &t.Goal, &status, &plan,
			&t.StepsUsed, &t.StepBudget, &summary, &findings, &question,
			&lastErr, &checkpoint, &t.TokensUsed, &parentTaskID, &findingRecords, &t.CreatedAt, &t.UpdatedAt); err != nil {
			slog.Warn("agent: skipping corrupted row", "error", err)
			continue
		}
		t.Status = TaskStatus(status)
		t.Summary = summary.String
		t.Question = question.String
		t.LastError = lastErr.String
		t.ParentTaskID = parentTaskID.String
		if plan.Valid && plan.String != "" {
			if err := json.Unmarshal([]byte(plan.String), &t.Plan); err != nil {
				slog.Warn("agent: plan unmarshal failed", "task", t.ID, "error", err)
			}
		}
		if findings.Valid && findings.String != "" {
			if err := json.Unmarshal([]byte(findings.String), &t.Findings); err != nil {
				slog.Warn("agent: findings unmarshal failed", "task", t.ID, "error", err)
			}
		}
		if findingRecords.Valid && findingRecords.String != "" {
			if err := json.Unmarshal([]byte(findingRecords.String), &t.FindingRecords); err != nil {
				slog.Warn("agent: finding_records unmarshal failed", "task", t.ID, "error", err)
			}
		}
		if checkpoint.Valid && checkpoint.String != "" && checkpoint.String != "null" {
			if err := json.Unmarshal([]byte(checkpoint.String), &t.Checkpoint); err != nil {
				slog.Warn("agent: checkpoint unmarshal failed", "task", t.ID, "error", err)
			}
		}
		s.items[t.ID] = t
	}
	return rows.Err()
}
