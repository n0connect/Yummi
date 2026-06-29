// Package execution provides an execution lifecycle store for approved proposals.
// Records are persisted to SQLite when a database is provided; in-memory only otherwise.
package execution

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/n0connect/yummi/orchestrator/internal/security/threatscan"
)

const (
	// StatusQueued means the execution request is accepted but not yet started.
	StatusQueued = "queued"
	// StatusRunning means the execution request is currently being processed.
	StatusRunning = "running"
	// StatusCompleted means the execution flow finished successfully.
	StatusCompleted = "completed"
	// StatusFailed means the execution flow failed.
	StatusFailed = "failed"
	// StatusCanceled means the execution was canceled by the user.
	StatusCanceled = "canceled"
)

const defaultWaitTimeout = 3 * time.Second
const defaultProcessDelay = 300 * time.Millisecond

var (
	// ErrStoreNil indicates that the execution store receiver is nil.
	ErrStoreNil = errors.New("execution store is nil")
	// ErrCommandRequired indicates enqueue was called with an empty command.
	ErrCommandRequired = errors.New("command is required")
	// ErrExecutionNotFound indicates there is no execution for the provided id.
	ErrExecutionNotFound = errors.New("execution not found")
	// ErrExecutionTerminal indicates the execution is already in a terminal state.
	ErrExecutionTerminal = errors.New("execution already terminal")
)

// Record describes one execution lifecycle item.
type Record struct {
	ID         string `json:"id"`
	SessionID  string `json:"session_id"`
	ProposalID string `json:"proposal_id"`
	Command    string `json:"command"`
	Mode       string `json:"mode"`
	Status     string `json:"status"`
	Output     string `json:"output,omitempty"`
	Error      string `json:"error,omitempty"`
	// SecurityBanner is the content-layer threat verdict for this command's
	// output: a non-empty warning string when the raw output trips known
	// prompt-injection / C2 / exfiltration patterns, "" when clean. It is
	// DERIVED from Output (never persisted as authoritative state — recomputed
	// on load) and computed ONCE here at the store chokepoint so that BOTH
	// execution paths inherit it: the agent loop AND the single-command chat
	// path. Consumers (UI, any LLM re-feed) must surface it so the model treats
	// the output as untrusted DATA, not instructions. Output itself is left raw
	// for the audit record — the banner is a separate signal, never mutates it.
	SecurityBanner string `json:"security_banner,omitempty"`
	// SanitizedOutput/SanitizedError are ANSI/C1-escape-stripped copies of
	// Output/Error (threatscan.StripANSI), DERIVED the same way SecurityBanner
	// is — never persisted as authoritative state, recomputed on load. Output
	// itself stays raw for the operator's audit record (a researcher may
	// legitimately need to see attacker write-ups verbatim, escapes included).
	// But any consumer that re-feeds this content back into the LLM (the UI's
	// chat feedback loop, any future server-side re-feed) MUST use the
	// sanitized copy, mirroring how the agent loop already strips ANSI from
	// its own observations (agent/graph.go) before reusing the same prompt
	// channel — otherwise a target could use escape sequences to smuggle a
	// payload past the banner scan and back into model context.
	SanitizedOutput string `json:"sanitized_output,omitempty"`
	SanitizedError  string `json:"sanitized_error,omitempty"`
	CreatedAt       int64  `json:"created_at"`
	UpdatedAt       int64  `json:"updated_at"`
}

// Event is published whenever an execution lifecycle changes.
type Event struct {
	Type      string `json:"type"`
	Timestamp int64  `json:"timestamp"`
	Record    Record `json:"record"`
}

// ProcessFunc is called to execute a command. It receives the record context
// and should return the command output or an error.
type ProcessFunc func(ctx context.Context, rec Record) (output string, err error)

// Store keeps execution records with optional SQLite persistence.
type Store struct {
	mu           sync.RWMutex
	items        map[string]Record
	active       map[string]context.CancelFunc
	db           *sql.DB // nil = in-memory only
	processDelay time.Duration
	subscribers  map[int]chan Event
	nextSubID    int
	processFn    ProcessFunc
}

// NewStore creates an in-memory-only execution store (suitable for tests).
func NewStore() *Store {
	return &Store{
		items:        make(map[string]Record),
		active:       make(map[string]context.CancelFunc),
		processDelay: defaultProcessDelay,
		subscribers:  make(map[int]chan Event),
	}
}

// NewStoreWithDB creates a persistent store backed by the provided SQLite database.
// Active (non-terminal) records from previous runs are reloaded into memory on startup.
func NewStoreWithDB(db *sql.DB) *Store {
	s := &Store{
		items:        make(map[string]Record),
		active:       make(map[string]context.CancelFunc),
		db:           db,
		processDelay: defaultProcessDelay,
		subscribers:  make(map[int]chan Event),
	}
	// Best-effort reload — ignore errors (table may not exist yet on first run)
	_ = s.loadFromDB()
	return s
}

// SetProcessor registers a function to handle actual command execution.
// When set, process() delegates to this function instead of the dry-run stub.
func (s *Store) SetProcessor(fn ProcessFunc) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.processFn = fn
	s.mu.Unlock()
}

// Enqueue creates a new execution and starts stub processing in background.
func (s *Store) Enqueue(sessionID, proposalID, command, mode string) (Record, error) {
	if s == nil {
		return Record{}, ErrStoreNil
	}
	if command == "" {
		return Record{}, ErrCommandRequired
	}
	if mode == "" {
		mode = "manual_terminal"
	}
	if sessionID == "" {
		sessionID = "default"
	}

	now := time.Now().Unix()
	rec := Record{
		ID:         uuid.NewString(),
		SessionID:  sessionID,
		ProposalID: proposalID,
		Command:    command,
		Mode:       mode,
		Status:     StatusQueued,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	s.mu.Lock()
	s.items[rec.ID] = rec
	s.mu.Unlock()
	s.publish(Event{
		Type:      "execution.queued",
		Timestamp: rec.UpdatedAt,
		Record:    rec,
	})
	s.dbPersist(rec)

	go s.process(rec.ID)
	return rec, nil
}

// Cancel requests cancellation for a queued/running execution.
func (s *Store) Cancel(id string) (Record, error) {
	if s == nil {
		return Record{}, ErrStoreNil
	}
	if id == "" {
		return Record{}, fmt.Errorf("%w: empty id", ErrExecutionNotFound)
	}

	now := time.Now().Unix()
	var cancel context.CancelFunc
	s.mu.Lock()
	rec, ok := s.items[id]
	if !ok {
		s.mu.Unlock()
		return Record{}, fmt.Errorf("%w: %s", ErrExecutionNotFound, id)
	}
	if IsTerminalStatus(rec.Status) {
		s.mu.Unlock()
		return Record{}, fmt.Errorf("%w: %s (%s)", ErrExecutionTerminal, id, rec.Status)
	}
	rec.Status = StatusCanceled
	rec.UpdatedAt = now
	if rec.Output == "" {
		rec.Output = "Execution canceled by user."
	}
	s.items[id] = rec
	cancel = s.active[id]
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	s.publish(Event{
		Type:      "execution.canceled",
		Timestamp: rec.UpdatedAt,
		Record:    rec,
	})
	s.dbPersist(rec)
	return rec, nil
}

// Get returns one execution record by id.
// Falls back to the database when the record is not in memory (e.g. after restart).
func (s *Store) Get(id string) (Record, bool) {
	if s == nil {
		return Record{}, false
	}
	s.mu.RLock()
	rec, ok := s.items[id]
	s.mu.RUnlock()
	if ok {
		return rec, true
	}
	// DB fallback
	if s.db == nil {
		return Record{}, false
	}
	var r Record
	var proposalID, output, errText sql.NullString
	err := s.db.QueryRow(`
		SELECT id,session_id,proposal_id,command,mode,status,output,error,created_at,updated_at
		FROM executions WHERE id=?`, id).Scan(
		&r.ID, &r.SessionID, &proposalID, &r.Command, &r.Mode,
		&r.Status, &output, &errText, &r.CreatedAt, &r.UpdatedAt,
	)
	if err != nil {
		return Record{}, false
	}
	r.ProposalID = proposalID.String
	r.Output = output.String
	r.Error = errText.String
	return r, true
}

// ListBySession returns executions for one session, latest first.
func (s *Store) ListBySession(sessionID string, limit int) []Record {
	if s == nil {
		return nil
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	if sessionID == "" {
		sessionID = "default"
	}

	s.mu.RLock()
	out := make([]Record, 0, len(s.items))
	for _, rec := range s.items {
		if rec.SessionID == sessionID {
			out = append(out, rec)
		}
	}
	s.mu.RUnlock()

	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt == out[j].CreatedAt {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt > out[j].CreatedAt
	})

	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// WaitForTerminal waits until an execution reaches a terminal status.
func (s *Store) WaitForTerminal(id string, timeout time.Duration) (Record, error) {
	if s == nil {
		return Record{}, ErrStoreNil
	}
	if id == "" {
		return Record{}, fmt.Errorf("%w: empty id", ErrExecutionNotFound)
	}
	if timeout <= 0 {
		timeout = defaultWaitTimeout
	}

	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(15 * time.Millisecond)
	defer ticker.Stop()

	for {
		rec, ok := s.Get(id)
		if !ok {
			return Record{}, fmt.Errorf("%w: %s", ErrExecutionNotFound, id)
		}
		if IsTerminalStatus(rec.Status) {
			return rec, nil
		}
		if time.Now().After(deadline) {
			return Record{}, fmt.Errorf("wait timeout: execution %s still %s", id, rec.Status)
		}
		<-ticker.C
	}
}

func (s *Store) process(id string) {
	running, ctx, cancel, ok := s.startRunning(id)
	if !ok {
		return
	}
	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.active, id)
		s.mu.Unlock()
	}()
	s.publish(Event{
		Type:      "execution.running",
		Timestamp: running.UpdatedAt,
		Record:    running,
	})
	s.dbPersist(running)

	// Use injected processor if available, otherwise fall back to stub.
	s.mu.RLock()
	fn := s.processFn
	s.mu.RUnlock()

	if fn != nil {
		output, err := fn(ctx, running)
		if err != nil {
			errText := err.Error()
			failed, ok := s.updateStatusIfIn(id, []string{StatusRunning}, StatusFailed, output, errText)
			if ok {
				s.publish(Event{
					Type:      "execution.failed",
					Timestamp: failed.UpdatedAt,
					Record:    failed,
				})
				s.dbPersist(failed)
			}
			return
		}
		completed, ok := s.updateStatusIfIn(id, []string{StatusRunning}, StatusCompleted, output, "")
		if ok {
			s.publish(Event{
				Type:      "execution.completed",
				Timestamp: completed.UpdatedAt,
				Record:    completed,
			})
			s.dbPersist(completed)
		}
		return
	}

	// Stub fallback: no direct command execution.
	timer := time.NewTimer(s.processDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}
	completed, ok := s.updateStatusIfIn(id, []string{StatusRunning}, StatusCompleted, "Dry-run only. Execute approved command in terminal panel.", "")
	if !ok {
		return
	}
	s.publish(Event{
		Type:      "execution.completed",
		Timestamp: completed.UpdatedAt,
		Record:    completed,
	})
	s.dbPersist(completed)
}

func (s *Store) startRunning(id string) (Record, context.Context, context.CancelFunc, bool) {
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now().Unix()

	s.mu.Lock()
	rec, ok := s.items[id]
	if !ok {
		s.mu.Unlock()
		cancel()
		return Record{}, nil, nil, false
	}
	if rec.Status != StatusQueued {
		s.mu.Unlock()
		cancel()
		return Record{}, nil, nil, false
	}
	rec.Status = StatusRunning
	rec.UpdatedAt = now
	s.items[id] = rec
	s.active[id] = cancel
	s.mu.Unlock()

	return rec, ctx, cancel, true
}

func (s *Store) updateStatusIfIn(id string, allowed []string, status, output, errText string) (Record, bool) {
	now := time.Now().Unix()
	s.mu.Lock()
	rec, ok := s.items[id]
	if !ok {
		s.mu.Unlock()
		return Record{}, false
	}
	if !statusAllowed(rec.Status, allowed) {
		s.mu.Unlock()
		return Record{}, false
	}
	rec.Status = status
	rec.UpdatedAt = now
	if output != "" {
		rec.Output = output
		// Content-layer fail-closed at the single chokepoint: every command's
		// output — agent-loop OR single-command chat — originates on a (hostile)
		// target and is adversarial by default. Scan it once here so no consumer
		// can forget to. Detection only (a banner), never blocking or mutating
		// the raw output: the operator can't intervene mid-command and a
		// researcher may legitimately be reading attacker write-ups.
		rec.SecurityBanner = threatscan.Banner(output)
		rec.SanitizedOutput = threatscan.StripANSI(output)
	}
	if errText != "" {
		rec.Error = errText
		// Failure output (stderr / error body) is equally attacker-influenced.
		// Only set a banner from the error when the output itself was clean, so
		// we never clobber a hit found on stdout.
		if rec.SecurityBanner == "" {
			rec.SecurityBanner = threatscan.Banner(errText)
		}
		rec.SanitizedError = threatscan.StripANSI(errText)
	}
	s.items[id] = rec
	s.mu.Unlock()
	return rec, true
}

// IsTerminalStatus reports whether a lifecycle status is final.
func IsTerminalStatus(status string) bool {
	switch status {
	case StatusCompleted, StatusFailed, StatusCanceled:
		return true
	default:
		return false
	}
}

func statusAllowed(current string, allowed []string) bool {
	return slices.Contains(allowed, current)
}

// Subscribe registers a listener for lifecycle events.
func (s *Store) Subscribe(buffer int) (int, <-chan Event, error) {
	if s == nil {
		return 0, nil, ErrStoreNil
	}
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

// Unsubscribe removes a previously registered listener.
func (s *Store) Unsubscribe(id int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.subscribers, id)
	s.mu.Unlock()
	// Intentionally do not close subscriber channel here.
	// publish() snapshots sinks outside the lock; closing here can race with send.
}

func (s *Store) publish(evt Event) {
	if s == nil {
		return
	}
	s.mu.RLock()
	sinks := make([]chan Event, 0, len(s.subscribers))
	for _, ch := range s.subscribers {
		sinks = append(sinks, ch)
	}
	s.mu.RUnlock()

	for _, ch := range sinks {
		select {
		case ch <- evt:
		default:
			// Drop when consumer is slow; lifecycle can still be queried via REST.
		}
	}
}

// ─── SQLite persistence helpers ───────────────────────────────────────────────

// dbPersist upserts a record into the executions table. No-op when db is nil.
func (s *Store) dbPersist(rec Record) {
	if s.db == nil {
		return
	}
	_, _ = s.db.Exec(`
		INSERT INTO executions(id,session_id,proposal_id,command,mode,status,output,error,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			status=excluded.status,
			output=excluded.output,
			error=excluded.error,
			updated_at=excluded.updated_at`,
		rec.ID, rec.SessionID, rec.ProposalID, rec.Command, rec.Mode,
		rec.Status, rec.Output, rec.Error, rec.CreatedAt, rec.UpdatedAt,
	)
}

// loadFromDB restores all records from the database into the in-memory map.
// Called once at startup via NewStoreWithDB.
func (s *Store) loadFromDB() error {
	if s.db == nil {
		return nil
	}
	rows, err := s.db.Query(`
		SELECT id,session_id,proposal_id,command,mode,status,output,error,created_at,updated_at
		FROM executions ORDER BY created_at ASC`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	s.mu.Lock()
	defer s.mu.Unlock()
	for rows.Next() {
		var rec Record
		var proposalID, output, errText sql.NullString
		if err := rows.Scan(
			&rec.ID, &rec.SessionID, &proposalID, &rec.Command, &rec.Mode,
			&rec.Status, &output, &errText, &rec.CreatedAt, &rec.UpdatedAt,
		); err != nil {
			continue
		}
		rec.ProposalID = proposalID.String
		rec.Output = output.String
		rec.Error = errText.String
		// SecurityBanner/SanitizedOutput/SanitizedError are derived, not stored —
		// recompute them on load so a restored record carries the same
		// content-layer verdict and LLM-safe copy it had live.
		if rec.Output != "" {
			rec.SecurityBanner = threatscan.Banner(rec.Output)
			rec.SanitizedOutput = threatscan.StripANSI(rec.Output)
		} else if rec.Error != "" {
			rec.SecurityBanner = threatscan.Banner(rec.Error)
		}
		if rec.Error != "" {
			rec.SanitizedError = threatscan.StripANSI(rec.Error)
		}
		s.items[rec.ID] = rec
	}
	return rows.Err()
}
