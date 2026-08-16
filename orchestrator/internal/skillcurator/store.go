// Package skillcurator drafts candidate skills from successful task
// trajectories, scans them with the same content-layer threat scan skill.LoadDir
// already applies to hand-authored skill YAML, and quarantines them pending
// explicit operator approval. It never writes to the live, loaded skill
// registry itself — that happens only via the REST approval handler, after a
// human decision. Depends on skill (for the Skill type) and threatscan; is
// depended on by agent (via a plain function-value callback, not an import
// inside the graph machinery) and api — never the reverse, so there is no
// import cycle with agent.
package skillcurator

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// PendingSkill is one curator-drafted skill awaiting (or past) operator review.
type PendingSkill struct {
	ID               string `json:"id"`
	Status           string `json:"status"` // pending | activating | approved | rejected
	Origin           string `json:"origin"` // provenance tag, matches knowledge.PersistentContext.Origin convention
	TaskID           string `json:"task_id"`
	SessionID        string `json:"session_id"`
	Name             string `json:"name"`
	DraftYAML        string `json:"draft_yaml"`
	Trajectory       string `json:"trajectory"` // JSON: the observations/tactics this draft was derived from
	ThreatScanResult string `json:"threat_scan_result,omitempty"`
	RejectReason     string `json:"reject_reason,omitempty"`
	CreatedAt        int64  `json:"created_at"`
	ResolvedAt       int64  `json:"resolved_at,omitempty"`
}

// Store owns the pending_skills table. db may be nil (in-memory map, tests).
type Store struct {
	mu   sync.RWMutex
	rows map[string]PendingSkill
	db   *sql.DB

	subscribers map[int]chan Event
	nextSubID   int
}

// Event is broadcast to subscribers whenever a pending skill draft is
// created or its status changes (curator-drafted, approved, rejected, or
// rolled back to pending after a failed activation). Mirrors
// agent.Store.Event's "namespace.status" Type convention exactly, so
// api.HandleEventsWS can forward it over /ws/events the same way it already
// forwards task.* and execution.* events. Fired on every Put/Resolve/
// ClaimActivation/CompleteActivation write so the UI never has to poll for
// review-queue changes.
type Event struct {
	Type      string       `json:"type"` // "skill." + PendingSkill.Status, e.g. "skill.pending", "skill.rejected"
	Timestamp int64        `json:"timestamp"`
	Skill     PendingSkill `json:"skill"`
}

// Subscribe registers a new listener for pending-skill lifecycle events.
func (s *Store) Subscribe(buffer int) (int, <-chan Event, error) {
	if buffer <= 0 {
		buffer = 32
	}
	ch := make(chan Event, buffer)
	s.mu.Lock()
	if s.subscribers == nil {
		s.subscribers = make(map[int]chan Event)
	}
	id := s.nextSubID
	s.nextSubID++
	s.subscribers[id] = ch
	s.mu.Unlock()
	return id, ch, nil
}

// Unsubscribe removes a listener. The channel is intentionally not closed
// (publish snapshots sinks outside the lock; see agent.Store for the same
// pattern) — the caller simply stops reading from it.
func (s *Store) Unsubscribe(id int) {
	s.mu.Lock()
	delete(s.subscribers, id)
	s.mu.Unlock()
}

func (s *Store) publish(ps PendingSkill) {
	evt := Event{Type: "skill." + ps.Status, Timestamp: time.Now().Unix(), Skill: ps}
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

const schema = `
CREATE TABLE IF NOT EXISTS pending_skills (
    id TEXT PRIMARY KEY,
    status TEXT NOT NULL DEFAULT 'pending',
    origin TEXT NOT NULL DEFAULT 'auto',
    task_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    name TEXT NOT NULL,
    draft_yaml TEXT NOT NULL,
    trajectory TEXT NOT NULL,
    threat_scan_result TEXT,
    reject_reason TEXT,
    created_at INTEGER NOT NULL,
    resolved_at INTEGER
);
CREATE INDEX IF NOT EXISTS idx_pending_skills_status ON pending_skills(status);
CREATE INDEX IF NOT EXISTS idx_pending_skills_task ON pending_skills(task_id);
`

// NewStore creates a skillcurator store. db may be nil (map-backed, tests).
func NewStore(db *sql.DB) (*Store, error) {
	s := &Store{rows: make(map[string]PendingSkill), db: db}
	if db != nil {
		if _, err := db.Exec(schema); err != nil {
			return nil, fmt.Errorf("skillcurator schema: %w", err)
		}
		if err := s.loadAll(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// ClaimActivation atomically reserves a pending draft for one approval
// request. It is not yet approved: file persistence and registry activation
// must both succeed before CompleteActivation commits the final state.
func (s *Store) ClaimActivation(ctx context.Context, id string) (PendingSkill, error) {
	s.mu.Lock()
	ps, ok := s.rows[id]
	if !ok {
		s.mu.Unlock()
		return PendingSkill{}, fmt.Errorf("pending skill %s not found", id)
	}
	if ps.Status != "pending" {
		s.mu.Unlock()
		return PendingSkill{}, fmt.Errorf("pending skill %s cannot activate (status=%s)", id, ps.Status)
	}
	if s.db != nil {
		res, err := s.db.ExecContext(ctx, `UPDATE pending_skills SET status='activating', reject_reason=NULL WHERE id=? AND status='pending'`, id)
		if err != nil {
			s.mu.Unlock()
			return PendingSkill{}, fmt.Errorf("claim skill activation: %w", err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			s.mu.Unlock()
			return PendingSkill{}, fmt.Errorf("pending skill %s was claimed concurrently", id)
		}
	}
	ps.Status = "activating"
	ps.RejectReason = ""
	s.rows[id] = ps
	s.mu.Unlock()
	// Deliberately NOT published: "activating" is a transient internal
	// reservation state invisible to the pending-review list (which only
	// ever queries status=pending), so a subscriber refetching in response
	// would see no change. CompleteActivation publishes the resulting
	// approved/pending state instead.
	return ps, nil
}

// CompleteActivation commits activating->approved, or rolls it back to
// pending with an operator-visible error when persistence/activation failed.
func (s *Store) CompleteActivation(ctx context.Context, id string, activationErr error) (PendingSkill, error) {
	s.mu.Lock()
	ps, ok := s.rows[id]
	if !ok {
		s.mu.Unlock()
		return PendingSkill{}, fmt.Errorf("pending skill %s not found", id)
	}
	if ps.Status != "activating" {
		s.mu.Unlock()
		return PendingSkill{}, fmt.Errorf("pending skill %s is not activating (status=%s)", id, ps.Status)
	}
	if activationErr != nil {
		ps.Status = "pending"
		ps.RejectReason = "activation failed: " + activationErr.Error()
		ps.ResolvedAt = 0
	} else {
		ps.Status = "approved"
		ps.RejectReason = ""
		ps.ResolvedAt = time.Now().Unix()
	}
	if s.db != nil {
		res, err := s.db.ExecContext(ctx,
			`UPDATE pending_skills SET status=?,reject_reason=?,resolved_at=? WHERE id=? AND status='activating'`,
			ps.Status, nullStr(ps.RejectReason), nullInt(ps.ResolvedAt), id)
		if err != nil {
			s.mu.Unlock()
			return PendingSkill{}, fmt.Errorf("complete skill activation: %w", err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			s.mu.Unlock()
			return PendingSkill{}, fmt.Errorf("pending skill %s activation state changed concurrently", id)
		}
	}
	s.rows[id] = ps
	s.mu.Unlock()
	s.publish(ps)
	return ps, nil
}

// Put inserts a pending-skill row. ID and CreatedAt are filled when empty.
func (s *Store) Put(ctx context.Context, ps PendingSkill) error {
	if ps.ID == "" {
		ps.ID = uuid.NewString()
	}
	if ps.CreatedAt == 0 {
		ps.CreatedAt = time.Now().Unix()
	}
	if ps.Status == "" {
		ps.Status = "pending"
	}
	if s.db != nil {
		_, err := s.db.ExecContext(ctx,
			`INSERT INTO pending_skills(id,status,origin,task_id,session_id,name,draft_yaml,trajectory,threat_scan_result,reject_reason,created_at,resolved_at)
			 VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
			ps.ID, ps.Status, ps.Origin, ps.TaskID, ps.SessionID, ps.Name, ps.DraftYAML, ps.Trajectory,
			nullStr(ps.ThreatScanResult), nullStr(ps.RejectReason), ps.CreatedAt, nullInt(ps.ResolvedAt))
		if err != nil {
			return fmt.Errorf("put pending skill: %w", err)
		}
	}
	s.mu.Lock()
	s.rows[ps.ID] = ps
	s.mu.Unlock()
	s.publish(ps)
	return nil
}

// Get returns a single pending-skill row.
func (s *Store) Get(ctx context.Context, id string) (PendingSkill, bool, error) {
	if s.db != nil {
		row := s.db.QueryRowContext(ctx,
			`SELECT id,status,origin,task_id,session_id,name,draft_yaml,trajectory,
			        COALESCE(threat_scan_result,''),COALESCE(reject_reason,''),created_at,COALESCE(resolved_at,0)
			 FROM pending_skills WHERE id=?`, id)
		var ps PendingSkill
		if err := scanPendingSkill(row, &ps); err != nil {
			if err == sql.ErrNoRows {
				return PendingSkill{}, false, nil
			}
			return PendingSkill{}, false, fmt.Errorf("get pending skill: %w", err)
		}
		return ps, true, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	ps, ok := s.rows[id]
	return ps, ok, nil
}

// List returns pending-skill rows matching status, newest first.
func (s *Store) List(ctx context.Context, status string) ([]PendingSkill, error) {
	if s.db != nil {
		rows, err := s.db.QueryContext(ctx,
			`SELECT id,status,origin,task_id,session_id,name,draft_yaml,trajectory,
			        COALESCE(threat_scan_result,''),COALESCE(reject_reason,''),created_at,COALESCE(resolved_at,0)
			 FROM pending_skills WHERE status=? ORDER BY created_at DESC`, status)
		if err != nil {
			return nil, fmt.Errorf("list pending skills: %w", err)
		}
		defer func() { _ = rows.Close() }()
		out := make([]PendingSkill, 0)
		for rows.Next() {
			var ps PendingSkill
			if err := scanPendingSkillRows(rows, &ps); err != nil {
				slog.Warn("skillcurator: scan pending skill", "error", err)
				continue
			}
			out = append(out, ps)
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("list pending skills: %w", err)
		}
		return out, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]PendingSkill, 0)
	for _, ps := range s.rows {
		if ps.Status == status {
			out = append(out, ps)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out, nil
}

// Resolve transitions a pending row to approved or rejected. Resolving a
// missing or already-resolved row is an error (no silent double-resolve).
// The whole check-then-write is atomic: s.mu is held for the entire method
// (not just the final write), and the DB path additionally guards the UPDATE
// with `WHERE status='pending'` + a RowsAffected check. Without this, two
// concurrent approvals of the same row could both observe status="pending"
// and both proceed — letting a racing REST handler write the skill file and
// hot-add it to the live registry twice.
func (s *Store) Resolve(ctx context.Context, id string, approved bool, reason string) (PendingSkill, error) {
	s.mu.Lock()

	ps, ok := s.rows[id]
	if !ok {
		s.mu.Unlock()
		return PendingSkill{}, fmt.Errorf("pending skill %s not found", id)
	}
	if ps.Status != "pending" {
		s.mu.Unlock()
		return PendingSkill{}, fmt.Errorf("pending skill %s already resolved (status=%s)", id, ps.Status)
	}
	if approved {
		ps.Status = "approved"
	} else {
		ps.Status = "rejected"
		ps.RejectReason = reason
	}
	ps.ResolvedAt = time.Now().Unix()

	if s.db != nil {
		res, err := s.db.ExecContext(ctx,
			`UPDATE pending_skills SET status=?, reject_reason=?, resolved_at=? WHERE id=? AND status='pending'`,
			ps.Status, nullStr(ps.RejectReason), ps.ResolvedAt, id)
		if err != nil {
			s.mu.Unlock()
			return PendingSkill{}, fmt.Errorf("resolve pending skill: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			s.mu.Unlock()
			return PendingSkill{}, fmt.Errorf("resolve pending skill: %w", err)
		}
		if n == 0 {
			s.mu.Unlock()
			return PendingSkill{}, fmt.Errorf("pending skill %s already resolved", id)
		}
	}
	s.rows[id] = ps
	s.mu.Unlock()
	s.publish(ps)
	return ps, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanPendingSkill(row *sql.Row, ps *PendingSkill) error {
	return scanInto(row, ps)
}

func scanPendingSkillRows(rows *sql.Rows, ps *PendingSkill) error {
	return scanInto(rows, ps)
}

func scanInto(r rowScanner, ps *PendingSkill) error {
	return r.Scan(&ps.ID, &ps.Status, &ps.Origin, &ps.TaskID, &ps.SessionID, &ps.Name,
		&ps.DraftYAML, &ps.Trajectory, &ps.ThreatScanResult, &ps.RejectReason, &ps.CreatedAt, &ps.ResolvedAt)
}

func (s *Store) loadAll() error {
	rows, err := s.db.Query(`SELECT id,status,origin,task_id,session_id,name,draft_yaml,trajectory,
		COALESCE(threat_scan_result,''),COALESCE(reject_reason,''),created_at,COALESCE(resolved_at,0)
		FROM pending_skills`)
	if err != nil {
		return fmt.Errorf("load pending skills: %w", err)
	}
	var interrupted []PendingSkill
	for rows.Next() {
		var ps PendingSkill
		if err := scanPendingSkillRows(rows, &ps); err != nil {
			return fmt.Errorf("load pending skill: %w", err)
		}
		// A process crash during activation never leaves a false approval.
		// Return the draft to pending so the operator may retry safely.
		if ps.Status == "activating" {
			ps.Status = "pending"
			ps.RejectReason = "activation interrupted by orchestrator restart"
			ps.ResolvedAt = 0
			interrupted = append(interrupted, ps)
		}
		s.rows[ps.ID] = ps
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	for _, ps := range interrupted {
		_, _ = s.db.Exec(`UPDATE pending_skills SET status='pending',reject_reason=?,resolved_at=NULL WHERE id=?`, ps.RejectReason, ps.ID)
	}
	return nil
}

func nullStr(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func nullInt(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}
