package knowledge

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

// Store owns the world-model tables. db may be nil (in-memory map, for tests).
type Store struct {
	mu       sync.RWMutex
	findings map[string]Finding // id -> finding (mirror of DB for nil-db path)
	targets  map[string]Target
	tactics  map[string]Tactic
	db       *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS targets (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    value TEXT NOT NULL,
    in_scope INTEGER NOT NULL DEFAULT 1,
    notes TEXT,
    created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS findings (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    target_id TEXT,
    severity TEXT NOT NULL,
    title TEXT NOT NULL,
    description TEXT,
    evidence TEXT,
    source TEXT NOT NULL DEFAULT 'manual',
    task_id TEXT,
    created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS procedural_memory (
    id TEXT PRIMARY KEY,
    tool TEXT NOT NULL,
    command_pattern TEXT NOT NULL,
    outcome TEXT NOT NULL,
    context_tags TEXT,
    note TEXT,
    hit_count INTEGER NOT NULL DEFAULT 1,
    last_used_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_k_targets_session ON targets(session_id);
CREATE INDEX IF NOT EXISTS idx_k_findings_session ON findings(session_id);
CREATE INDEX IF NOT EXISTS idx_k_procedural_tool ON procedural_memory(tool);
`

// NewStore creates a knowledge store. db may be nil (map-backed, tests).
// On a real db it creates its tables and migrates pre-existing findings
// rows that lack the provenance columns.
func NewStore(db *sql.DB) (*Store, error) {
	s := &Store{
		findings: make(map[string]Finding),
		targets:  make(map[string]Target),
		tactics:  make(map[string]Tactic),
		db:       db,
	}
	if db != nil {
		if _, err := db.Exec(schema); err != nil {
			return nil, fmt.Errorf("knowledge schema: %w", err)
		}
		// Migrate databases whose findings table predates provenance columns.
		_, _ = db.Exec(`ALTER TABLE findings ADD COLUMN source TEXT NOT NULL DEFAULT 'manual'`)
		_, _ = db.Exec(`ALTER TABLE findings ADD COLUMN task_id TEXT`)
	}
	return s, nil
}

// RecordFinding stores a finding. ID, CreatedAt, and a default Source are
// filled when empty.
func (s *Store) RecordFinding(ctx context.Context, f Finding) error {
	if f.ID == "" {
		f.ID = uuid.NewString()
	}
	if f.CreatedAt == 0 {
		f.CreatedAt = time.Now().Unix()
	}
	if f.Source == "" {
		f.Source = "manual"
	}
	if s.db != nil {
		_, err := s.db.ExecContext(ctx,
			`INSERT INTO findings(id,session_id,target_id,severity,title,description,evidence,source,task_id,created_at)
			 VALUES(?,?,?,?,?,?,?,?,?,?)`,
			f.ID, f.SessionID, nullStr(f.TargetID), f.Severity, f.Title, f.Description, f.Evidence, f.Source, nullStr(f.TaskID), f.CreatedAt)
		if err != nil {
			return fmt.Errorf("record finding: %w", err)
		}
	}
	s.mu.Lock()
	s.findings[f.ID] = f
	s.mu.Unlock()
	return nil
}

// ListFindings returns a session's findings, newest first.
func (s *Store) ListFindings(sessionID string) []Finding {
	if s.db != nil {
		rows, err := s.db.Query(
			`SELECT id,session_id,COALESCE(target_id,''),severity,title,COALESCE(description,''),
			        COALESCE(evidence,''),COALESCE(source,'manual'),COALESCE(task_id,''),created_at
			 FROM findings WHERE session_id=? ORDER BY created_at DESC`, sessionID)
		if err != nil {
			slog.Error("knowledge: list findings", "error", err)
			return nil
		}
		defer func() { _ = rows.Close() }()
		out := make([]Finding, 0)
		for rows.Next() {
			var f Finding
			if err := rows.Scan(&f.ID, &f.SessionID, &f.TargetID, &f.Severity, &f.Title,
				&f.Description, &f.Evidence, &f.Source, &f.TaskID, &f.CreatedAt); err != nil {
				slog.Warn("knowledge: scan finding", "error", err)
				continue
			}
			out = append(out, f)
		}
		return out
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Finding, 0)
	for _, f := range s.findings {
		if f.SessionID == sessionID {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out
}

// CreateTarget stores an in-scope target.
func (s *Store) CreateTarget(ctx context.Context, t Target) error {
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	if t.CreatedAt == 0 {
		t.CreatedAt = time.Now().Unix()
	}
	if s.db != nil {
		inScope := 0
		if t.InScope {
			inScope = 1
		}
		_, err := s.db.ExecContext(ctx,
			`INSERT INTO targets(id,session_id,value,in_scope,notes,created_at) VALUES(?,?,?,?,?,?)`,
			t.ID, t.SessionID, t.Value, inScope, t.Notes, t.CreatedAt)
		if err != nil {
			return fmt.Errorf("create target: %w", err)
		}
	}
	s.mu.Lock()
	s.targets[t.ID] = t
	s.mu.Unlock()
	return nil
}

// ListTargets returns a session's targets, oldest first.
func (s *Store) ListTargets(sessionID string) []Target {
	if s.db != nil {
		rows, err := s.db.Query(
			`SELECT id,session_id,value,in_scope,COALESCE(notes,''),created_at
			 FROM targets WHERE session_id=? ORDER BY created_at`, sessionID)
		if err != nil {
			slog.Error("knowledge: list targets", "error", err)
			return nil
		}
		defer func() { _ = rows.Close() }()
		out := make([]Target, 0)
		for rows.Next() {
			var t Target
			var inScope int
			if err := rows.Scan(&t.ID, &t.SessionID, &t.Value, &inScope, &t.Notes, &t.CreatedAt); err != nil {
				slog.Warn("knowledge: scan target", "error", err)
				continue
			}
			t.InScope = inScope != 0
			out = append(out, t)
		}
		return out
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Target, 0)
	for _, t := range s.targets {
		if t.SessionID == sessionID {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
