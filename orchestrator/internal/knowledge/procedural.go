package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/n0connect/yummi/orchestrator/internal/scope"
	"github.com/n0connect/yummi/orchestrator/internal/security/threatscan"
)

// procedureCandidateCap bounds how many rows are pulled per tool before
// ranking — procedural_memory dedups on (tool, command_pattern, outcome), so
// the distinct-pattern count per tool stays small in practice; this is a
// safety ceiling, not a tuning knob.
const procedureCandidateCap = 500

// maxTacticComplexity caps how many extra tokens (beyond the tool name) count
// toward a tactic's complexity weight, so one absurdly long one-off command
// can't dominate ranking forever.
const maxTacticComplexity = 6

// tacticScore ranks a tactic by how useful it is to recall, not by raw
// repetition. A trivial always-succeed command (id, whoami, pwd) run 50 times
// used to outrank a specific, technique-carrying command (nmap -sV -p-
// <TARGET>) run 3 times under plain hit_count DESC — the trivial command
// isn't actually saving the model any rediscovery work. Two adjustments:
//  1. hit_count is log-scaled, so repetition still matters but can't grow
//     without bound relative to complexity.
//  2. command_pattern complexity (token count beyond the tool name, capped)
//     multiplies the score, rewarding tactics that encode an actual approach
//     (flags, args) over bare tool invocations.
//
// A "worked" outcome gets a modest edge over "failed" at equal hit_count/
// complexity — recalling a successful approach is more valuable than
// recalling a dead end, though failed tactics remain visible (useful as
// "don't repeat this").
func tacticScore(t Tactic) float64 {
	complexity := min(strings.Count(strings.TrimSpace(t.CommandPattern), " "), maxTacticComplexity)
	weight := (1.0 + float64(complexity)) * outcomeWeight(t.Outcome)
	return math.Log2(float64(t.HitCount)+1) * weight
}

// outcomeWeight is the shared "a proven approach beats a dead end" bonus,
// applied both to tacticScore (structured retrieval) and to semantic
// retrieval's cosine-similarity ranking (embeddings.go's NearestTactics) — a
// single source of truth so the two retrieval strategies agree on how much a
// tactic's real-world outcome should matter, instead of drifting to two
// different magic numbers. Failed tactics remain visible (useful as "don't
// repeat this"), just not ranked above a worked one at equal relevance.
func outcomeWeight(outcome string) float64 {
	if outcome == "worked" {
		return 1.5
	}
	return 1.0
}

// rankTactics sorts candidates by tacticScore descending, tie-broken by
// recency, and truncates to limit.
func rankTactics(candidates []Tactic, limit int) []Tactic {
	sort.SliceStable(candidates, func(i, j int) bool {
		si, sj := tacticScore(candidates[i]), tacticScore(candidates[j])
		if si != sj {
			return si > sj
		}
		return candidates[i].LastUsedAt > candidates[j].LastUsedAt
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates
}

// targetPlaceholder replaces the concrete target token in a stored command
// pattern so no target-specific data enters the portable procedural layer.
const targetPlaceholder = "<TARGET>"

// RecordTactic scrubs the command's target, then upserts a procedural-memory
// row keyed by (tool, command_pattern, outcome): on repeat it bumps hit_count
// instead of inserting a duplicate. A command whose extracted target cannot be
// removed from the string is skipped (fail-closed for privacy).
func (s *Store) RecordTactic(ctx context.Context, command, outcome string, tags []string, note string) error {
	tool := firstToken(command)
	if tool == "" {
		return nil // nothing to learn from an empty command
	}
	pattern, ok := scrubTarget(command)
	if !ok {
		return nil // fail-closed: could not guarantee target removal
	}
	// Content-layer fail-closed: command_pattern/note are rendered verbatim
	// into a future task's MEMORY prompt block (retrieval.go), the same
	// cross-task prompt-injection persistence vector recordTaskFindings
	// already guards findings against. ScopeContext (detect, don't
	// hard-block on borderline patterns) since this is agent-authored
	// content, not user-mediated like a skill file.
	if msg := threatscan.FirstThreatMessage(pattern+"\n"+note+"\n"+strings.Join(tags, "\n"), threatscan.ScopeContext); msg != "" {
		slog.Warn("knowledge: tactic rejected by threat scan", "tool", tool, "reason", msg)
		return nil
	}
	now := time.Now().Unix()
	tagsJSON, _ := json.Marshal(tags)

	if s.db != nil {
		// Try to bump an existing identical tactic.
		res, err := s.db.ExecContext(ctx,
			`UPDATE procedural_memory SET hit_count=hit_count+1, last_used_at=?
			 WHERE tool=? AND command_pattern=? AND outcome=?`,
			now, tool, pattern, outcome)
		if err != nil {
			return fmt.Errorf("bump tactic: %w", err)
		}
		id := uuid.NewString()
		if n, _ := res.RowsAffected(); n == 0 {
			_, err = s.db.ExecContext(ctx,
				`INSERT INTO procedural_memory(id,tool,command_pattern,outcome,context_tags,note,hit_count,last_used_at,created_at)
				 VALUES(?,?,?,?,?,?,?,?,?)`,
				id, tool, pattern, outcome, string(tagsJSON), note, 1, now, now)
			if err != nil {
				return fmt.Errorf("insert tactic: %w", err)
			}
		} else {
			// Bumped an existing row: re-fetch its id for the embed-on-write hook
			// below (a bare UPDATE doesn't return the row's id).
			_ = s.db.QueryRowContext(ctx,
				`SELECT id FROM procedural_memory WHERE tool=? AND command_pattern=? AND outcome=?`,
				tool, pattern, outcome).Scan(&id)
		}
		s.embedAndSave(ctx, id, pattern)
		return nil
	}

	// In-memory path: dedup by composite key.
	s.mu.Lock()
	for id, tac := range s.tactics {
		if tac.Tool == tool && tac.CommandPattern == pattern && tac.Outcome == outcome {
			tac.HitCount++
			tac.LastUsedAt = now
			s.tactics[id] = tac
			s.mu.Unlock()
			s.embedAndSave(ctx, id, pattern)
			return nil
		}
	}
	id := uuid.NewString()
	s.tactics[id] = Tactic{
		ID: id, Tool: tool, CommandPattern: pattern, Outcome: outcome,
		ContextTags: tags, Note: note, HitCount: 1, LastUsedAt: now, CreatedAt: now,
	}
	s.mu.Unlock()
	s.embedAndSave(ctx, id, pattern)
	return nil
}

// ListTactics returns tactics for a tool, ranked by tacticScore (usefulness
// to recall, not raw repetition — see tacticScore) descending.
func (s *Store) ListTactics(tool string, limit int) []Tactic {
	if limit <= 0 {
		limit = 10
	}
	if s.db != nil {
		// Pull a generous, unranked candidate pool (bounded by
		// procedureCandidateCap) and rank in Go — a SQL ORDER BY hit_count DESC
		// LIMIT would truncate BEFORE complexity/outcome are weighed in, so a
		// low-hit-count-but-high-value tactic could be cut before it's ever
		// scored.
		rows, err := s.db.Query(
			`SELECT id,tool,command_pattern,outcome,COALESCE(context_tags,''),COALESCE(note,''),hit_count,last_used_at,created_at
			 FROM procedural_memory WHERE tool=? LIMIT ?`, tool, procedureCandidateCap)
		if err != nil {
			return nil
		}
		defer func() { _ = rows.Close() }()
		out := make([]Tactic, 0)
		for rows.Next() {
			var tac Tactic
			var tagsJSON string
			if err := rows.Scan(&tac.ID, &tac.Tool, &tac.CommandPattern, &tac.Outcome,
				&tagsJSON, &tac.Note, &tac.HitCount, &tac.LastUsedAt, &tac.CreatedAt); err != nil {
				continue
			}
			if tagsJSON != "" {
				_ = json.Unmarshal([]byte(tagsJSON), &tac.ContextTags)
			}
			out = append(out, tac)
		}
		if err := rows.Err(); err != nil {
			return nil
		}
		return rankTactics(out, limit)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Tactic, 0)
	for _, tac := range s.tactics {
		if tac.Tool == tool {
			out = append(out, tac)
		}
	}
	return rankTactics(out, limit)
}

// scrubTarget replaces the command's target token with the placeholder.
// Returns ok=false (fail-closed) when a target was extracted but the literal
// token is not found in the string for replacement.
func scrubTarget(command string) (string, bool) {
	target := scope.ExtractTarget(command)
	if target == "" {
		return command, true // no target to remove
	}
	if !strings.Contains(command, target) {
		return "", false // extracted a target we cannot locate to remove — refuse
	}
	return strings.ReplaceAll(command, target, targetPlaceholder), true
}

// firstToken returns the first whitespace-delimited token (the tool name).
func firstToken(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}
