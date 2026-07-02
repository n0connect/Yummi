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
	Status           string `json:"status"` // pending | approved | rejected
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
	}
	return s, nil
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
func (s *Store) Resolve(ctx context.Context, id string, approved bool, reason string) (PendingSkill, error) {
	ps, ok, err := s.Get(ctx, id)
	if err != nil {
		return PendingSkill{}, err
	}
	if !ok {
		return PendingSkill{}, fmt.Errorf("pending skill %s not found", id)
	}
	if ps.Status != "pending" {
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
		_, err := s.db.ExecContext(ctx,
			`UPDATE pending_skills SET status=?, reject_reason=?, resolved_at=? WHERE id=?`,
			ps.Status, nullStr(ps.RejectReason), ps.ResolvedAt, id)
		if err != nil {
			return PendingSkill{}, fmt.Errorf("resolve pending skill: %w", err)
		}
	}
	s.mu.Lock()
	s.rows[id] = ps
	s.mu.Unlock()
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
