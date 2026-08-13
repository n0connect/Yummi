// Package execution provides an execution lifecycle store for approved proposals.
// Records are persisted to SQLite when a database is provided; in-memory only otherwise.
package execution

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/n0connect/yummi/orchestrator/internal/scope"
	"github.com/n0connect/yummi/orchestrator/internal/security/threatscan"
)

const (
	// StatusQueued means the execution request is accepted but not yet started.
	StatusQueued = "queued"
	// StatusRunning means the execution request is currently being processed.
	StatusRunning = "running"
	// StatusAwaitingEgressApproval means the gateway placed the command's target
	// in the "needs_approval" egress verdict (Ask mode) and the execution is
	// held — fail-closed, no traffic — until the operator approves or denies the
	// external access via ResolveEgress. Non-terminal: it resumes (→ queued) on
	// approval or ends (→ failed) on denial.
	StatusAwaitingEgressApproval = "awaiting_egress_approval"
	// StatusAwaitingThreatApproval means an assistant-loop command completed but
	// its target-controlled output matched the content threat scanner. The output
	// is retained for the operator, but is not returned to the conductor until
	// the operator explicitly approves sharing it.
	StatusAwaitingThreatApproval = "awaiting_threat_approval"
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
	// ErrNotAwaitingEgress indicates ResolveEgress was called on an execution
	// that is not currently held for egress approval.
	ErrNotAwaitingEgress = errors.New("execution is not awaiting egress approval")
	// ErrNotAwaitingThreat indicates ResolveThreatApproval was called on an
	// execution whose output is not currently held by the content gate.
	ErrNotAwaitingThreat = errors.New("execution is not awaiting threat approval")
	// ErrEgressApprovalRequired is the sentinel a ProcessFunc returns to signal
	// that the command's target needs operator egress approval (gateway Ask
	// mode). The store reacts by holding the execution in
	// StatusAwaitingEgressApproval instead of failing it. The executor's own
	// executor.ErrEgressApprovalRequired (from RunScopedCommand) is translated
	// to this sentinel by executor.ProcessFunc — the store deliberately does not
	// import executor.
	ErrEgressApprovalRequired = errors.New("egress approval required")
)

// EgressApprovalRequiredError represents a detailed error when egress approval is required,
// carrying the specific IPs that were resolved at the check time to prevent TOCTOU.
type EgressApprovalRequiredError struct {
	Target string
	IPs    []string
}

func (e EgressApprovalRequiredError) Error() string {
	return fmt.Sprintf("egress approval required: target %s (IPs: %v)", e.Target, e.IPs)
}

func (e EgressApprovalRequiredError) Is(target error) bool {
	return target == ErrEgressApprovalRequired || target.Error() == "egress approval required"
}

// Record describes one execution lifecycle item.
type Record struct {
	ID              string `json:"id"`
	SessionID       string `json:"session_id"`
	ProposalID      string `json:"proposal_id"`
	Command         string `json:"command"`
	Mode            string `json:"mode"`
	Status          string `json:"status"`
	Output          string `json:"output,omitempty"`
	Error           string `json:"error,omitempty"`
	SecurityBanner  string `json:"security_banner,omitempty"`
	SanitizedOutput string `json:"sanitized_output,omitempty"`
	SanitizedError  string `json:"sanitized_error,omitempty"`
	EgressTarget    string `json:"egress_target,omitempty"`
	PinnedIPs       string `json:"pinned_ips,omitempty"`
	// MCPTool/MCPArgs are set only for a record dispatched through the
	// sandbox-resident MCP server (internal/sandboxmcp) instead of a raw
	// shell command — empty for every existing shell-command record. Command
	// still holds a human-readable rendering (sandboxmcp.RenderCommand) so
	// the UI/audit trail shows a real command line either way.
	MCPTool   string `json:"mcp_tool,omitempty"`
	MCPArgs   string `json:"mcp_args,omitempty"` // json-encoded map[string]any
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
	// ElapsedSecs is set only on periodic "still running" heartbeat events
	// (see process()'s heartbeatInterval) — never persisted, never present on
	// a terminal record. It exists purely so the UI can show live elapsed
	// time instead of an unchanging "running" state for however long a real
	// command (nikto, a full nmap scan, ...) takes, which previously looked
	// indistinguishable from a hang.
	ElapsedSecs int64 `json:"elapsed_secs,omitempty"`
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
	mu                sync.RWMutex
	items             map[string]Record
	active            map[string]context.CancelFunc
	db                *sql.DB // nil = in-memory only
	processDelay      time.Duration
	heartbeatInterval time.Duration
	subscribers       map[int]chan Event
	nextSubID         int
	processFn         ProcessFunc
}

// NewStore creates an in-memory-only execution store (suitable for tests).
func NewStore() *Store {
	return &Store{
		items:             make(map[string]Record),
		active:            make(map[string]context.CancelFunc),
		processDelay:      defaultProcessDelay,
		heartbeatInterval: defaultHeartbeatInterval,
		subscribers:       make(map[int]chan Event),
	}
}

// NewStoreWithDB creates a persistent store backed by the provided SQLite database.
// Active (non-terminal) records from previous runs are reloaded into memory on startup.
func NewStoreWithDB(db *sql.DB) *Store {
	s := &Store{
		items:             make(map[string]Record),
		active:            make(map[string]context.CancelFunc),
		db:                db,
		processDelay:      defaultProcessDelay,
		heartbeatInterval: defaultHeartbeatInterval,
		subscribers:       make(map[int]chan Event),
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

// EnqueueMCP queues an MCP tool call — the structured-argument sibling of
// Enqueue used by the sandbox-resident-MCP dispatch path. args is
// JSON-encoded into Record.MCPArgs so Record stays a flat, DB-persistable
// struct; displayCommand becomes Record.Command so audit/UI/approval show a
// real command line exactly as the shell path does.
func (s *Store) EnqueueMCP(sessionID, proposalID, tool string, args map[string]any, displayCommand, mode string) (Record, error) {
	if s == nil {
		return Record{}, ErrStoreNil
	}
	if tool == "" {
		return Record{}, fmt.Errorf("%w: mcp tool name required", ErrCommandRequired)
	}
	if displayCommand == "" {
		displayCommand = tool
	}
	if mode == "" {
		mode = "manual_terminal"
	}
	if sessionID == "" {
		sessionID = "default"
	}
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return Record{}, fmt.Errorf("marshal mcp args: %w", err)
	}

	now := time.Now().Unix()
	rec := Record{
		ID:         uuid.NewString(),
		SessionID:  sessionID,
		ProposalID: proposalID,
		Command:    displayCommand,
		Mode:       mode,
		Status:     StatusQueued,
		MCPTool:    tool,
		MCPArgs:    string(argsJSON),
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

// holdForEgressApproval transitions a running execution into
// StatusAwaitingEgressApproval and records the (derived) target and resolved IPs.
// Returns (record, true) only when the execution was running; a no-op otherwise.
func (s *Store) holdForEgressApproval(id, target string, ips []string) (Record, bool) {
	now := time.Now().Unix()
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.items[id]
	if !ok || rec.Status != StatusRunning {
		return Record{}, false
	}
	rec.Status = StatusAwaitingEgressApproval
	rec.EgressTarget = target
	if len(ips) > 0 {
		if buf, err := json.Marshal(ips); err == nil {
			rec.PinnedIPs = string(buf)
		}
	}
	rec.UpdatedAt = now
	s.items[id] = rec
	return rec, true
}

// ResolveEgress settles an execution held in StatusAwaitingEgressApproval.
//
// On approval, unlock is invoked (the API layer wires this to a gateway
// temporary-allow rule for the target — the store itself holds no gateway
// client, keeping the conductor/executor/store boundary intact); if it
// succeeds the execution is requeued and re-processed (the gateway now returns
// "allow" for the freshly-opened target). On denial — or if unlock fails — the
// execution ends as failed. Returns ErrNotAwaitingEgress if the execution is
// not currently held.
func (s *Store) ResolveEgress(id string, approved bool, unlock func() error) (Record, error) {
	if s == nil {
		return Record{}, ErrStoreNil
	}
	rec, ok := s.Get(id)
	if !ok {
		return Record{}, fmt.Errorf("%w: %s", ErrExecutionNotFound, id)
	}
	if rec.Status != StatusAwaitingEgressApproval {
		return Record{}, fmt.Errorf("%w: %s (%s)", ErrNotAwaitingEgress, id, rec.Status)
	}

	if !approved {
		failed, _ := s.failEgress(id, "operator denied external network access for "+rec.EgressTarget)
		return failed, nil
	}
	if unlock != nil {
		if err := unlock(); err != nil {
			failed, _ := s.failEgress(id, "egress approval could not be applied: "+err.Error())
			return failed, err
		}
	}

	requeued, ok := s.updateStatusIfIn(id, []string{StatusAwaitingEgressApproval}, StatusQueued, "", "")
	if !ok {
		return Record{}, fmt.Errorf("%w: %s", ErrNotAwaitingEgress, id)
	}
	requeued.EgressTarget = ""
	s.mu.Lock()
	s.items[id] = requeued
	s.mu.Unlock()
	s.publish(Event{Type: "execution.queued", Timestamp: requeued.UpdatedAt, Record: requeued})
	s.dbPersist(requeued)
	go s.process(id)
	return requeued, nil
}

func (s *Store) failEgress(id, reason string) (Record, bool) {
	failed, ok := s.updateStatusIfIn(id, []string{StatusAwaitingEgressApproval}, StatusFailed, "", reason)
	if !ok {
		return Record{}, false
	}
	s.publish(Event{Type: "execution.failed", Timestamp: failed.UpdatedAt, Record: failed})
	s.dbPersist(failed)
	return failed, true
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
	var proposalID, output, errText, pinnedIPs sql.NullString
	err := s.db.QueryRow(`
		SELECT id,session_id,proposal_id,command,mode,status,output,error,pinned_ips,created_at,updated_at
		FROM executions WHERE id=?`, id).Scan(
		&r.ID, &r.SessionID, &proposalID, &r.Command, &r.Mode,
		&r.Status, &output, &errText, &pinnedIPs, &r.CreatedAt, &r.UpdatedAt,
	)
	if err != nil {
		return Record{}, false
	}
	r.ProposalID = proposalID.String
	r.Output = output.String
	r.Error = errText.String
	r.PinnedIPs = pinnedIPs.String
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
		output, err := s.runWithHeartbeat(ctx, id, running, fn)
		if err != nil {
			// Egress Ask mode: the gateway wants operator approval before the
			// target may be reached. Hold the execution (fail-closed, no
			// traffic) instead of failing it — ResolveEgress resumes or ends it.
			if errors.Is(err, ErrEgressApprovalRequired) {
				var ips []string
				target := scope.ExtractTarget(running.Command)
				var egressErr EgressApprovalRequiredError
				if errors.As(err, &egressErr) {
					target = egressErr.Target
					ips = egressErr.IPs
				}
				held, ok := s.holdForEgressApproval(id, target, ips)
				if ok {
					s.publish(Event{
						Type:      "execution.awaiting_egress_approval",
						Timestamp: held.UpdatedAt,
						Record:    held,
					})
					s.dbPersist(held)
				}
				return
			}
			errText := err.Error()
			failed, ok := s.finishOrHoldForThreat(id, StatusFailed, output, errText)
			if ok {
				eventType := "execution.failed"
				if failed.Status == StatusAwaitingThreatApproval {
					eventType = "execution.awaiting_threat_approval"
				}
				s.publish(Event{
					Type:      eventType,
					Timestamp: failed.UpdatedAt,
					Record:    failed,
				})
				s.dbPersist(failed)
			}
			return
		}
		completed, ok := s.finishOrHoldForThreat(id, StatusCompleted, output, "")
		if ok {
			eventType := "execution.completed"
			if completed.Status == StatusAwaitingThreatApproval {
				eventType = "execution.awaiting_threat_approval"
			}
			s.publish(Event{
				Type:      eventType,
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

// defaultHeartbeatInterval controls how often runWithHeartbeat publishes a
// "still running" progress event while the underlying ProcessFunc call is
// blocked. A real command (nikto, a full nmap scan, ...) can legitimately
// take minutes; without this, the UI has nothing to show between
// "execution.running" and the eventual "execution.completed" and a long,
// silent wait looks indistinguishable from a hang. Overridable per-Store
// (like processDelay) so tests don't need to wait multiple seconds.
const defaultHeartbeatInterval = 3 * time.Second

// runWithHeartbeat runs fn in the background and periodically republishes the
// execution's "running" event with ElapsedSecs set, until fn returns. It does
// not change fn's contract (still one blocking call, same signature) — the
// heartbeat is purely additive observability, not real output streaming.
func (s *Store) runWithHeartbeat(ctx context.Context, id string, running Record, fn ProcessFunc) (string, error) {
	type outcome struct {
		output string
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		output, err := fn(ctx, running)
		done <- outcome{output, err}
	}()

	ticker := time.NewTicker(s.heartbeatInterval)
	defer ticker.Stop()
	start := time.Now()

	for {
		select {
		case res := <-done:
			return res.output, res.err
		case <-ticker.C:
			rec, ok := s.Get(id)
			if !ok || rec.Status != StatusRunning {
				continue
			}
			rec.ElapsedSecs = int64(time.Since(start).Seconds())
			s.publish(Event{
				Type:      "execution.running",
				Timestamp: time.Now().Unix(),
				Record:    rec,
			})
		}
	}
}

// finishOrHoldForThreat records an execution result and pauses the
// single-command assistant loop when its target-controlled output is flagged.
// Agent tasks have their own graph-level threat_hold node, so holding them here
// would deadlock that graph while it waits for a terminal execution result.
func (s *Store) finishOrHoldForThreat(id, finalStatus, output, errText string) (Record, bool) {
	rec, ok := s.updateStatusIfIn(id, []string{StatusRunning}, finalStatus, output, errText)
	if !ok || rec.Mode != "assistant_loop" || rec.SecurityBanner == "" {
		return rec, ok
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.items[id]
	if !ok || current.Status != finalStatus {
		return Record{}, false
	}
	current.Status = StatusAwaitingThreatApproval
	current.UpdatedAt = time.Now().Unix()
	s.items[id] = current
	return current, true
}

// ResolveThreatApproval releases or rejects output held by the content-layer
// gate. Approval restores the original terminal outcome (completed/failed);
// rejection cancels only the model-feedback step -- the command has already
// run and its raw output remains visible to the operator and audit log.
func (s *Store) ResolveThreatApproval(id string, approved bool) (Record, error) {
	if s == nil {
		return Record{}, ErrStoreNil
	}
	s.mu.Lock()
	rec, ok := s.items[id]
	if !ok {
		s.mu.Unlock()
		return Record{}, fmt.Errorf("%w: %s", ErrExecutionNotFound, id)
	}
	if rec.Status != StatusAwaitingThreatApproval {
		s.mu.Unlock()
		return Record{}, fmt.Errorf("%w: %s (%s)", ErrNotAwaitingThreat, id, rec.Status)
	}
	if approved {
		if rec.Error != "" {
			rec.Status = StatusFailed
		} else {
			rec.Status = StatusCompleted
		}
	} else {
		rec.Status = StatusCanceled
	}
	rec.UpdatedAt = time.Now().Unix()
	s.items[id] = rec
	s.mu.Unlock()

	eventType := "execution.threat_rejected"
	if approved {
		if rec.Status == StatusFailed {
			eventType = "execution.failed"
		} else {
			eventType = "execution.completed"
		}
	}
	s.publish(Event{Type: eventType, Timestamp: rec.UpdatedAt, Record: rec})
	s.dbPersist(rec)
	return rec, nil
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
		rec.SanitizedOutput = threatscan.StripANSI(output)
		rec.SecurityBanner = threatscan.Banner(rec.SanitizedOutput)
	}
	if errText != "" {
		rec.Error = errText
		// Failure output (stderr / error body) is equally attacker-influenced.
		// Only set a banner from the error when the output itself was clean, so
		// we never clobber a hit found on stdout.
		rec.SanitizedError = threatscan.StripANSI(errText)
		if rec.SecurityBanner == "" {
			rec.SecurityBanner = threatscan.Banner(rec.SanitizedError)
		}
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
		INSERT INTO executions(id,session_id,proposal_id,command,mode,status,output,error,pinned_ips,mcp_tool,mcp_args,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			status=excluded.status,
			output=excluded.output,
			error=excluded.error,
			pinned_ips=excluded.pinned_ips,
			mcp_tool=excluded.mcp_tool,
			mcp_args=excluded.mcp_args,
			updated_at=excluded.updated_at`,
		rec.ID, rec.SessionID, rec.ProposalID, rec.Command, rec.Mode,
		rec.Status, rec.Output, rec.Error, rec.PinnedIPs, rec.MCPTool, rec.MCPArgs, rec.CreatedAt, rec.UpdatedAt,
	)
}

// loadFromDB restores all records from the database into the in-memory map.
// Called once at startup via NewStoreWithDB.
func (s *Store) loadFromDB() error {
	if s.db == nil {
		return nil
	}
	rows, err := s.db.Query(`
		SELECT id,session_id,proposal_id,command,mode,status,output,error,pinned_ips,mcp_tool,mcp_args,created_at,updated_at
		FROM executions ORDER BY created_at ASC`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	s.mu.Lock()
	defer s.mu.Unlock()
	for rows.Next() {
		var rec Record
		var proposalID, output, errText, pinnedIPs, mcpTool, mcpArgs sql.NullString
		if err := rows.Scan(
			&rec.ID, &rec.SessionID, &proposalID, &rec.Command, &rec.Mode,
			&rec.Status, &output, &errText, &pinnedIPs, &mcpTool, &mcpArgs, &rec.CreatedAt, &rec.UpdatedAt,
		); err != nil {
			continue
		}
		rec.ProposalID = proposalID.String
		rec.Output = output.String
		rec.Error = errText.String
		rec.PinnedIPs = pinnedIPs.String
		rec.MCPTool = mcpTool.String
		rec.MCPArgs = mcpArgs.String
		// SecurityBanner/SanitizedOutput/SanitizedError are derived, not stored —
		// recompute them on load so a restored record carries the same
		// content-layer verdict and LLM-safe copy it had live.
		if rec.Output != "" {
			rec.SanitizedOutput = threatscan.StripANSI(rec.Output)
			rec.SecurityBanner = threatscan.Banner(rec.SanitizedOutput)
		} else if rec.Error != "" {
			rec.SecurityBanner = threatscan.Banner(threatscan.StripANSI(rec.Error))
		}
		if rec.Error != "" {
			rec.SanitizedError = threatscan.StripANSI(rec.Error)
		}
		// EgressTarget is derived, not stored — re-derive it for a record still
		// held for egress approval so a post-restart resolve can name the target.
		if rec.Status == StatusAwaitingEgressApproval {
			rec.EgressTarget = scope.ExtractTarget(rec.Command)
		}
		s.items[rec.ID] = rec
	}
	return rows.Err()
}
