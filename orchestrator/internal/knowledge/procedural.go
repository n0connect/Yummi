package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/n0connect/yummi/orchestrator/internal/scope"
)

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
		if n, _ := res.RowsAffected(); n == 0 {
			_, err = s.db.ExecContext(ctx,
				`INSERT INTO procedural_memory(id,tool,command_pattern,outcome,context_tags,note,hit_count,last_used_at,created_at)
				 VALUES(?,?,?,?,?,?,?,?,?)`,
				uuid.NewString(), tool, pattern, outcome, string(tagsJSON), note, 1, now, now)
			if err != nil {
				return fmt.Errorf("insert tactic: %w", err)
			}
		}
		return nil
	}

	// In-memory path: dedup by composite key.
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, tac := range s.tactics {
		if tac.Tool == tool && tac.CommandPattern == pattern && tac.Outcome == outcome {
			tac.HitCount++
			tac.LastUsedAt = now
			s.tactics[id] = tac
			return nil
		}
	}
	id := uuid.NewString()
	s.tactics[id] = Tactic{
		ID: id, Tool: tool, CommandPattern: pattern, Outcome: outcome,
		ContextTags: tags, Note: note, HitCount: 1, LastUsedAt: now, CreatedAt: now,
	}
	return nil
}

// ListTactics returns tactics for a tool, most-used/most-recent first.
func (s *Store) ListTactics(tool string, limit int) []Tactic {
	if limit <= 0 {
		limit = 10
	}
	if s.db != nil {
		rows, err := s.db.Query(
			`SELECT id,tool,command_pattern,outcome,COALESCE(context_tags,''),COALESCE(note,''),hit_count,last_used_at,created_at
			 FROM procedural_memory WHERE tool=? ORDER BY hit_count DESC, last_used_at DESC LIMIT ?`, tool, limit)
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
		return out
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Tactic, 0)
	for _, tac := range s.tactics {
		if tac.Tool == tool {
			out = append(out, tac)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].HitCount != out[j].HitCount {
			return out[i].HitCount > out[j].HitCount
		}
		return out[i].LastUsedAt > out[j].LastUsedAt
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
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
