package knowledge

import (
	"context"
	"fmt"
)

// Query asks for memory relevant to the current step.
type Query struct {
	SessionID string   // factual scope (current engagement)
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
// Kind is "finding" or "tactic".
type MemoryItem struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// RetrievalStrategy selects relevant memory for a step. StructuredRetrieval is
// the only implementation today (SQL keyed on session/tool). A future
// SemanticRetrieval (embeddings) implements this same interface WITHOUT any
// change to the agent or core.
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
	items := make([]MemoryItem, 0)

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
