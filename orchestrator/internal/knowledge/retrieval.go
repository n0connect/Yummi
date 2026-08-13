package knowledge

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// Query asks for memory relevant to the current step.
type Query struct {
	SessionID string   // factual scope (current engagement)
	Goal      string   // current task goal; drives context/finding relevance
	Tools     []string // tools relevant to the next step (procedural lookup)
	Limit     int      // max items per category (default 5)
	// ExcludeCommands are commands already executed in the CURRENT task. Their
	// scrubbed patterns are filtered out of the procedural results so memory
	// never echoes back a command the model just ran — that re-injection
	// ("use it, don't rediscover") is what drives weak models into command
	// loops. Procedural memory is for cross-task transfer, not for re-surfacing
	// the current task's own working set (which is already in the context).
	ExcludeCommands []string
}

// MemoryItem is one retrieved, pre-formatted memory line.
// Kind is "context", "finding", or "tactic".
type MemoryItem struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// RetrievalStrategy selects relevant memory for a step. StructuredRetrieval
// performs deterministic session/tool lookup; SemanticRetrieval ranks tactics
// with local embeddings and degrades to the structured strategy.
type RetrievalStrategy interface {
	Retrieve(ctx context.Context, q Query) ([]MemoryItem, error)
}

// StructuredRetrieval pulls factual findings for the session and procedural
// tactics for the named tools, deterministically and with no extra dependency.
type StructuredRetrieval struct {
	store *Store
}

// NewStructuredRetrieval wires the strategy to a store.
func NewStructuredRetrieval(s *Store) *StructuredRetrieval {
	return &StructuredRetrieval{store: s}
}

// Retrieve returns bounded factual + procedural memory for the query.
func (r *StructuredRetrieval) Retrieve(_ context.Context, q Query) ([]MemoryItem, error) {
	if r == nil || r.store == nil {
		return nil, nil
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 5
	}
	items := make([]MemoryItem, 0, limit*3)

	// Persistent context is the explicit "remember this" layer. Rank by goal
	// relevance, falling back to recency, and bound each value before it enters
	// a prompt. This closes the write-only REMEMBER gap: RecordContext data is
	// now actually recalled on later turns and after a process restart.
	for _, pc := range relevantContexts(r.store.ListContexts(q.SessionID), q.Goal, limit) {
		items = append(items, MemoryItem{
			Kind: "context",
			Text: fmt.Sprintf("[%s] %s", pc.Key, boundedMemoryText(pc.Value, 1200)),
		})
	}

	// Factual: prior findings for this session.
	findings := r.store.ListFindings(q.SessionID)
	for i, f := range findings {
		if i >= limit {
			break
		}
		items = append(items, MemoryItem{
			Kind: "finding",
			Text: fmt.Sprintf("[%s] %s — %s", f.Severity, f.Title, f.Description),
		})
	}

	// Build the set of scrubbed patterns the model already ran this task; these
	// are suppressed so memory never re-surfaces the current working set.
	excluded := make(map[string]struct{}, len(q.ExcludeCommands))
	for _, cmd := range q.ExcludeCommands {
		if pattern, ok := scrubTarget(cmd); ok {
			excluded[pattern] = struct{}{}
		}
	}

	// Procedural: tactics for each named tool, skipping current-task commands.
	for _, tool := range q.Tools {
		for _, tac := range r.store.ListTactics(tool, limit) {
			if _, skip := excluded[tac.CommandPattern]; skip {
				continue
			}
			items = append(items, MemoryItem{
				Kind: "tactic",
				Text: fmt.Sprintf("%s (%s, used %dx)", tac.CommandPattern, tac.Outcome, tac.HitCount),
			})
		}
	}
	return items, nil
}

// relevantContexts ranks session-scoped persistent contexts by lexical overlap
// with the goal. Recent contexts still remain recallable when the goal is too
// short or uses different terminology, which is important for explicit
// REMEMBER entries such as code maps and engagement constraints.
func relevantContexts(contexts []PersistentContext, goal string, limit int) []PersistentContext {
	if limit <= 0 {
		limit = 5
	}
	goalTerms := memoryTerms(goal)
	type scored struct {
		pc    PersistentContext
		score int
	}
	ranked := make([]scored, 0, len(contexts))
	for _, pc := range contexts {
		hay := memoryTerms(pc.Key + " " + pc.Value)
		score := 0
		for term := range goalTerms {
			if _, ok := hay[term]; ok {
				score++
			}
		}
		ranked = append(ranked, scored{pc: pc, score: score})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].pc.UpdatedAt > ranked[j].pc.UpdatedAt
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	out := make([]PersistentContext, len(ranked))
	for i := range ranked {
		out[i] = ranked[i].pc
	}
	return out
}

func memoryTerms(s string) map[string]struct{} {
	parts := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '_' && r != '-'
	})
	out := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		if len([]rune(part)) >= 3 {
			out[part] = struct{}{}
		}
	}
	return out
}

func boundedMemoryText(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "…"
}
