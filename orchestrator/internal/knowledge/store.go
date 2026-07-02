package knowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Store owns the world-model tables. db may be nil (in-memory map, for tests).
type Store struct {
	mu         sync.RWMutex
	findings   map[string]Finding // id -> finding (mirror of DB for nil-db path)
	targets    map[string]Target
	tactics    map[string]Tactic
	contexts   map[string]PersistentContext
	embeddings map[string]embeddingRow // key: embeddingKey(sourceTable, sourceID, model)
	embedder   Embedder                // optional; set via SetEmbedder, used by RecordTactic's embed-on-write hook
	embedModel string
	db         *sql.DB
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
CREATE TABLE IF NOT EXISTS persistent_context (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    origin TEXT NOT NULL DEFAULT 'agent',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_k_targets_session ON targets(session_id);
CREATE INDEX IF NOT EXISTS idx_k_findings_session ON findings(session_id);
CREATE INDEX IF NOT EXISTS idx_k_procedural_tool ON procedural_memory(tool);
CREATE INDEX IF NOT EXISTS idx_k_context_session ON persistent_context(session_id);
`

// NewStore creates a knowledge store. db may be nil (map-backed, tests).
// On a real db it creates its tables and migrates pre-existing findings
// rows that lack the provenance columns.
func NewStore(db *sql.DB) (*Store, error) {
	s := &Store{
		findings:   make(map[string]Finding),
		targets:    make(map[string]Target),
		tactics:    make(map[string]Tactic),
		contexts:   make(map[string]PersistentContext),
		embeddings: make(map[string]embeddingRow),
		db:         db,
	}
	if db != nil {
		if _, err := db.Exec(schema); err != nil {
			return nil, fmt.Errorf("knowledge schema: %w", err)
		}
		if _, err := db.Exec(embeddingsSchema); err != nil {
			return nil, fmt.Errorf("knowledge embeddings schema: %w", err)
		}
		// Migrate databases whose findings table predates provenance columns.
		_, _ = db.Exec(`ALTER TABLE findings ADD COLUMN source TEXT NOT NULL DEFAULT 'manual'`)
		_, _ = db.Exec(`ALTER TABLE findings ADD COLUMN task_id TEXT`)
		// Migrate persistent_context tables predating the memory-provenance column.
		_, _ = db.Exec(`ALTER TABLE persistent_context ADD COLUMN origin TEXT NOT NULL DEFAULT 'agent'`)
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

// RecordContext stores or updates a persistent memory element for the session.
func (s *Store) RecordContext(ctx context.Context, pc PersistentContext) error {
	if pc.ID == "" {
		pc.ID = uuid.NewString()
	}
	now := time.Now().Unix()
	if pc.CreatedAt == 0 {
		pc.CreatedAt = now
	}
	pc.UpdatedAt = now
	if pc.Origin == "" {
		pc.Origin = "agent"
	}

	if s.db != nil {
		_, err := s.db.ExecContext(ctx,
			`INSERT INTO persistent_context(id,session_id,key,value,origin,created_at,updated_at)
			 VALUES(?,?,?,?,?,?,?)
			 ON CONFLICT(id) DO UPDATE SET
			   value=excluded.value,
			   origin=excluded.origin,
			   updated_at=excluded.updated_at`,
			pc.ID, pc.SessionID, pc.Key, pc.Value, pc.Origin, pc.CreatedAt, pc.UpdatedAt)
		if err != nil {
			return fmt.Errorf("record context: %w", err)
		}
	}
	s.mu.Lock()
	s.contexts[pc.ID] = pc
	s.mu.Unlock()
	return nil
}

// ListContexts returns all persistent contexts for a session.
func (s *Store) ListContexts(sessionID string) []PersistentContext {
	if s.db != nil {
		rows, err := s.db.Query(
			`SELECT id,session_id,key,value,COALESCE(origin,'agent'),created_at,updated_at
			 FROM persistent_context WHERE session_id=? ORDER BY updated_at DESC`, sessionID)
		if err != nil {
			slog.Error("knowledge: list contexts", "error", err)
			return nil
		}
		defer func() { _ = rows.Close() }()
		out := make([]PersistentContext, 0)
		for rows.Next() {
			var pc PersistentContext
			if err := rows.Scan(&pc.ID, &pc.SessionID, &pc.Key, &pc.Value, &pc.Origin, &pc.CreatedAt, &pc.UpdatedAt); err != nil {
				slog.Warn("knowledge: scan context", "error", err)
				continue
			}
			out = append(out, pc)
		}
		return out
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]PersistentContext, 0)
	for _, pc := range s.contexts {
		if pc.SessionID == sessionID {
			out = append(out, pc)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out
}

// FindContext returns the persistent context for (sessionID, key), if any —
// used to update a code map in place instead of duplicating it.
func (s *Store) FindContext(sessionID, key string) (PersistentContext, bool) {
	for _, c := range s.ListContexts(sessionID) {
		if c.Key == key {
			return c, true
		}
	}
	return PersistentContext{}, false
}

// ListAllTactics returns the most-used procedural tactics across all tools.
func (s *Store) ListAllTactics(limit int) []Tactic {
	if limit <= 0 {
		limit = 50
	}
	if s.db == nil {
		s.mu.RLock()
		out := make([]Tactic, 0, len(s.tactics))
		for _, t := range s.tactics {
			out = append(out, t)
		}
		s.mu.RUnlock()
		sort.Slice(out, func(i, j int) bool { return out[i].HitCount > out[j].HitCount })
		if len(out) > limit {
			out = out[:limit]
		}
		return out
	}
	rows, err := s.db.Query(
		`SELECT id,tool,command_pattern,outcome,COALESCE(context_tags,''),COALESCE(note,''),hit_count,last_used_at,created_at
		 FROM procedural_memory ORDER BY hit_count DESC, last_used_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	out := make([]Tactic, 0)
	for rows.Next() {
		var t Tactic
		var tags string
		if err := rows.Scan(&t.ID, &t.Tool, &t.CommandPattern, &t.Outcome, &tags, &t.Note, &t.HitCount, &t.LastUsedAt, &t.CreatedAt); err != nil {
			continue
		}
		if tags != "" {
			_ = json.Unmarshal([]byte(tags), &t.ContextTags)
		}
		out = append(out, t)
	}
	return out
}
