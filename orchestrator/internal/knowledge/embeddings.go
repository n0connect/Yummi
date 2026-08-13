package knowledge

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"sort"

	"github.com/google/uuid"
)

// SetEmbedder configures the store to embed procedural-memory tactics on
// write (RecordTactic). Nil (or never calling this) leaves embedding
// disabled — RecordTactic behaves exactly as before, and SemanticRetrieval
// degrades to StructuredRetrieval.
func (s *Store) SetEmbedder(e Embedder, model string) {
	s.mu.Lock()
	s.embedder = e
	s.embedModel = model
	s.mu.Unlock()
}

// embedAndSave is a best-effort write-path hook: a failure to embed or save
// is logged and swallowed, never returned to the RecordTactic caller — an
// embedding gap degrades ranking quality (SemanticRetrieval falls back), it
// must never fail the agent's procedural-memory write.
func (s *Store) embedAndSave(ctx context.Context, sourceTable, sourceID, text string) {
	s.mu.RLock()
	embedder, model := s.embedder, s.embedModel
	s.mu.RUnlock()
	if embedder == nil {
		return
	}
	vec, err := embedder.Embed(ctx, text)
	if err != nil {
		slog.Warn("knowledge: embed-on-write failed (best-effort)", "error", err)
		return
	}
	if err := s.SaveEmbedding(ctx, sourceTable, sourceID, model, vec); err != nil {
		slog.Warn("knowledge: save embedding failed (best-effort)", "error", err)
	}
}

// NearestContexts returns session-scoped persistent contexts ordered by
// embedding similarity. Contexts below the relevance floor are omitted so a
// goal does not receive unrelated engagement notes merely to fill a quota.
func (s *Store) NearestContexts(ctx context.Context, model string, queryVec []float32, sessionID string, limit int) ([]PersistentContext, error) {
	if limit <= 0 {
		limit = 5
	}
	type scored struct {
		pc    PersistentContext
		score float64
	}
	var candidates []scored
	if s.db != nil {
		rows, err := s.db.QueryContext(ctx,
			`SELECT p.id,p.session_id,p.key,p.value,COALESCE(p.origin,'agent'),p.created_at,p.updated_at,e.vector
			 FROM embeddings e JOIN persistent_context p ON p.id=e.source_id
			 WHERE e.source_table='persistent_context' AND e.model=? AND p.session_id=? LIMIT ?`,
			model, sessionID, procedureCandidateCap)
		if err != nil {
			return nil, fmt.Errorf("nearest contexts: %w", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var pc PersistentContext
			var blob []byte
			if err := rows.Scan(&pc.ID, &pc.SessionID, &pc.Key, &pc.Value, &pc.Origin, &pc.CreatedAt, &pc.UpdatedAt, &blob); err != nil {
				continue
			}
			candidates = append(candidates, scored{pc: pc, score: cosineSimilarity(queryVec, decodeVector(blob))})
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("nearest contexts: %w", err)
		}
	} else {
		s.mu.RLock()
		for _, row := range s.embeddings {
			if row.SourceTable != "persistent_context" || row.Model != model {
				continue
			}
			pc, ok := s.contexts[row.SourceID]
			if !ok || pc.SessionID != sessionID {
				continue
			}
			candidates = append(candidates, scored{pc: pc, score: cosineSimilarity(queryVec, row.Vector)})
		}
		s.mu.RUnlock()
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].pc.UpdatedAt > candidates[j].pc.UpdatedAt
	})
	out := make([]PersistentContext, 0, min(limit, len(candidates)))
	for _, candidate := range candidates {
		if candidate.score < 0.35 {
			continue
		}
		out = append(out, candidate.pc)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

const embeddingsSchema = `
CREATE TABLE IF NOT EXISTS embeddings (
    id TEXT PRIMARY KEY,
    source_table TEXT NOT NULL,
    source_id TEXT NOT NULL,
    model TEXT NOT NULL,
    dim INTEGER NOT NULL,
    vector BLOB NOT NULL,
    created_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_embeddings_source ON embeddings(source_table, source_id, model);
`

// embeddingRow mirrors one embeddings table row (in-memory path).
type embeddingRow struct {
	SourceTable string
	SourceID    string
	Model       string
	Vector      []float32
}

// embeddingKey is the in-memory map key, matching the DB's unique index.
func embeddingKey(sourceTable, sourceID, model string) string {
	return sourceTable + ":" + sourceID + ":" + model
}

// SaveEmbedding stores (or replaces, on a repeat save for the same
// source_table/source_id/model) a vector for a knowledge-store row. Errors are
// returned to the caller, who treats a failed save as best-effort (a
// SemanticRetrieval strategy degrades to StructuredRetrieval on any embedding
// gap, never fails the agent loop).
func (s *Store) SaveEmbedding(ctx context.Context, sourceTable, sourceID, model string, vec []float32) error {
	if s.db != nil {
		blob := encodeVector(vec)
		_, err := s.db.ExecContext(ctx,
			`INSERT INTO embeddings(id,source_table,source_id,model,dim,vector,created_at)
			 VALUES(?,?,?,?,?,?,strftime('%s','now'))
			 ON CONFLICT(source_table,source_id,model) DO UPDATE SET
			   dim=excluded.dim, vector=excluded.vector, created_at=excluded.created_at`,
			uuid.NewString(), sourceTable, sourceID, model, len(vec), blob)
		if err != nil {
			return fmt.Errorf("save embedding: %w", err)
		}
	}
	s.mu.Lock()
	if s.embeddings == nil {
		s.embeddings = make(map[string]embeddingRow)
	}
	s.embeddings[embeddingKey(sourceTable, sourceID, model)] = embeddingRow{
		SourceTable: sourceTable, SourceID: sourceID, Model: model, Vector: vec,
	}
	s.mu.Unlock()
	return nil
}

// getEmbedding fetches a single stored vector, used by tests and callers that
// already know the exact source row.
func (s *Store) getEmbedding(sourceTable, sourceID, model string) ([]float32, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	row, ok := s.embeddings[embeddingKey(sourceTable, sourceID, model)]
	if !ok {
		return nil, false
	}
	return row.Vector, true
}

// minTacticSimilarity is the floor a candidate's outcome-weighted cosine score
// must clear to be returned at all. Found necessary via live testing: with
// tool="" (semantic retrieval's cross-tool generalization) and a short query
// text (often just a bare tool name, which embeds with little semantic
// signal), NearestTactics used to always pad its result up to `limit` with
// whatever was left over, however unrelated — a query about "dig" could
// surface a stored "cat /etc/passwd" tactic from a completely different task
// just because nothing better was available. Without a floor, "closest
// available" silently substitutes for "actually relevant," polluting the
// model's MEMORY prompt block with cross-session noise. 0.5 is deliberately
// conservative: better to return fewer, genuinely-related results (or none)
// than force-fill with noise.
const minTacticSimilarity = 0.5

// NearestTactics returns up to limit tactics for tool, ranked by cosine
// similarity of their stored embedding to queryVec, weighted by outcomeWeight
// (descending) — the same "a proven approach beats a dead end" preference
// tacticScore applies to the structured-retrieval path, so switching to
// semantic ranking for its cross-tool generalization doesn't silently let a
// merely-similar failed tactic outrank a less-similar worked one. Candidates
// scoring below minTacticSimilarity are dropped rather than used to pad the
// result up to limit — see its doc comment. Tactics with no saved embedding
// for model are excluded. An empty/missing embedding index returns an empty
// slice, not an error — callers (SemanticRetrieval) treat "nothing to rank" as
// a normal, non-fatal case.
func (s *Store) NearestTactics(ctx context.Context, model string, queryVec []float32, tool string, limit int) ([]Tactic, error) {
	if limit <= 0 {
		limit = 5
	}
	type scored struct {
		t     Tactic
		score float64
	}
	var candidates []scored

	if s.db != nil {
		// tool == "" means no tool filter: semantic retrieval's value-add over
		// StructuredRetrieval is cross-tool generalization (e.g. an nmap tactic
		// surfacing for a masscan query), so an empty tool must scan all tools,
		// not just skip filtering to zero rows.
		query := `SELECT p.id,p.tool,p.command_pattern,p.outcome,COALESCE(p.context_tags,''),COALESCE(p.note,''),
		                 p.hit_count,p.last_used_at,p.created_at,e.vector
		          FROM embeddings e JOIN procedural_memory p ON p.id = e.source_id
		          WHERE e.source_table='procedural_memory' AND e.model=?`
		args := []any{model}
		if tool != "" {
			query += ` AND p.tool=?`
			args = append(args, tool)
		}
		query += ` LIMIT ?`
		args = append(args, procedureCandidateCap)
		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("nearest tactics: %w", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var t Tactic
			var tags string
			var blob []byte
			if err := rows.Scan(&t.ID, &t.Tool, &t.CommandPattern, &t.Outcome, &tags, &t.Note,
				&t.HitCount, &t.LastUsedAt, &t.CreatedAt, &blob); err != nil {
				continue
			}
			if tags != "" {
				_ = json.Unmarshal([]byte(tags), &t.ContextTags)
			}
			candidates = append(candidates, scored{t, cosineSimilarity(queryVec, decodeVector(blob)) * outcomeWeight(t.Outcome)})
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("nearest tactics: %w", err)
		}
	} else {
		s.mu.RLock()
		for _, row := range s.embeddings {
			if row.SourceTable != "procedural_memory" || row.Model != model {
				continue
			}
			tac, ok := s.tactics[row.SourceID]
			if !ok || (tool != "" && tac.Tool != tool) {
				continue
			}
			candidates = append(candidates, scored{tac, cosineSimilarity(queryVec, row.Vector) * outcomeWeight(tac.Outcome)})
		}
		s.mu.RUnlock()
	}

	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })
	filtered := candidates[:0]
	for _, c := range candidates {
		if c.score >= minTacticSimilarity {
			filtered = append(filtered, c)
		}
	}
	candidates = filtered
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	out := make([]Tactic, len(candidates))
	for i, c := range candidates {
		out[i] = c.t
	}
	return out, nil
}

// NearestFindings returns up to limit findings for sessionID, ranked by
// cosine similarity of their stored embedding to queryVec (descending).
func (s *Store) NearestFindings(ctx context.Context, model string, queryVec []float32, sessionID string, limit int) ([]Finding, error) {
	if limit <= 0 {
		limit = 5
	}
	type scored struct {
		f     Finding
		score float64
	}
	var candidates []scored

	if s.db != nil {
		rows, err := s.db.QueryContext(ctx,
			`SELECT f.id,f.session_id,COALESCE(f.target_id,''),f.severity,f.title,COALESCE(f.description,''),
			        COALESCE(f.evidence,''),COALESCE(f.source,'manual'),COALESCE(f.task_id,''),f.created_at,e.vector
			 FROM embeddings e JOIN findings f ON f.id = e.source_id
			 WHERE e.source_table='findings' AND e.model=? AND f.session_id=?
			 LIMIT ?`, model, sessionID, procedureCandidateCap)
		if err != nil {
			return nil, fmt.Errorf("nearest findings: %w", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var f Finding
			var blob []byte
			if err := rows.Scan(&f.ID, &f.SessionID, &f.TargetID, &f.Severity, &f.Title, &f.Description,
				&f.Evidence, &f.Source, &f.TaskID, &f.CreatedAt, &blob); err != nil {
				continue
			}
			candidates = append(candidates, scored{f, cosineSimilarity(queryVec, decodeVector(blob))})
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("nearest findings: %w", err)
		}
	} else {
		s.mu.RLock()
		for _, row := range s.embeddings {
			if row.SourceTable != "findings" || row.Model != model {
				continue
			}
			f, ok := s.findings[row.SourceID]
			if !ok || f.SessionID != sessionID {
				continue
			}
			candidates = append(candidates, scored{f, cosineSimilarity(queryVec, row.Vector)})
		}
		s.mu.RUnlock()
	}

	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })
	out := make([]Finding, 0, min(limit, len(candidates)))
	for _, candidate := range candidates {
		if candidate.score < 0.35 {
			continue
		}
		out = append(out, candidate.f)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

// cosineSimilarity returns the cosine similarity of a and b in [-1, 1].
// Mismatched dimensions or a zero vector return 0 (treated as "unranked",
// not an error — the caller is a best-effort ranking, not a correctness gate).
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, magA, magB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		magA += float64(a[i]) * float64(a[i])
		magB += float64(b[i]) * float64(b[i])
	}
	if magA == 0 || magB == 0 {
		return 0
	}
	return dot / (math.Sqrt(magA) * math.Sqrt(magB))
}

// encodeVector serializes a float32 slice to a little-endian BLOB.
func encodeVector(vec []float32) []byte {
	buf := make([]byte, 4*len(vec))
	for i, f := range vec {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

// decodeVector deserializes a little-endian BLOB back to a float32 slice.
func decodeVector(blob []byte) []float32 {
	out := make([]float32, len(blob)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(blob[i*4:]))
	}
	return out
}
