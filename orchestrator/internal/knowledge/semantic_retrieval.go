package knowledge

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// Embedder is the small local seam SemanticRetrieval needs from an LLM
// client. Defined here (not imported from llm) so knowledge stays a leaf
// package — *llm.Client satisfies this structurally.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// SemanticRetrieval ranks procedural memory by embedding similarity instead
// of exact tool-name match, so a tactic recorded under one tool (e.g.
// "masscan") can surface for a query about a different-but-related tool
// (e.g. "nmap") — the generalization StructuredRetrieval cannot do. Findings
// are returned the same way StructuredRetrieval does (chronological); only
// tactics get embeddings written today (see Store.SetEmbedder).
//
// Degrades to fallback (StructuredRetrieval) whenever there is nothing to
// rank semantically: no embedder configured, an embed call fails, or the
// nearest-neighbor lookup itself errors. Never returns an error to the agent
// loop for a missing/broken embedding pipeline.
type SemanticRetrieval struct {
	store    *Store
	embedder Embedder
	model    string
	fallback RetrievalStrategy
}

// NewSemanticRetrieval wires the strategy to a store, an embedder, the
// embedding model tag rows were saved under, and a fallback strategy.
func NewSemanticRetrieval(s *Store, embedder Embedder, model string, fallback RetrievalStrategy) *SemanticRetrieval {
	return &SemanticRetrieval{store: s, embedder: embedder, model: model, fallback: fallback}
}

func (r *SemanticRetrieval) Retrieve(ctx context.Context, q Query) ([]MemoryItem, error) {
	if r == nil {
		return nil, nil
	}
	if r.embedder == nil || r.store == nil || r.model == "" {
		return r.retrieveFallback(ctx, q)
	}
	queryText := strings.TrimSpace(strings.Join(q.Tools, " "))
	if queryText == "" {
		return r.retrieveFallback(ctx, q)
	}
	vec, err := r.embedder.Embed(ctx, queryText)
	if err != nil {
		slog.Warn("knowledge: semantic retrieval embed failed, degrading to structured", "error", err)
		return r.retrieveFallback(ctx, q)
	}

	limit := q.Limit
	if limit <= 0 {
		limit = 5
	}
	items := make([]MemoryItem, 0)

	// Factual: same as StructuredRetrieval — findings aren't embedded (v1
	// scope: only procedural memory gets semantic ranking).
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

	excluded := make(map[string]struct{}, len(q.ExcludeCommands))
	for _, cmd := range q.ExcludeCommands {
		if pattern, ok := scrubTarget(cmd); ok {
			excluded[pattern] = struct{}{}
		}
	}

	// tool="" (no filter): the whole point of semantic ranking is cross-tool
	// recall, so we rank across ALL tools' embedded tactics, not just the
	// queried one. Pull extra candidates so exclusion filtering doesn't starve
	// the result below limit.
	tactics, err := r.store.NearestTactics(ctx, r.model, vec, "", limit+len(excluded))
	if err != nil {
		slog.Warn("knowledge: semantic tactic lookup failed, degrading to structured", "error", err)
		return r.retrieveFallback(ctx, q)
	}
	count := 0
	for _, tac := range tactics {
		if _, skip := excluded[tac.CommandPattern]; skip {
			continue
		}
		if count >= limit {
			break
		}
		items = append(items, MemoryItem{
			Kind: "tactic",
			Text: fmt.Sprintf("%s (%s, used %dx)", tac.CommandPattern, tac.Outcome, tac.HitCount),
		})
		count++
	}
	return items, nil
}

func (r *SemanticRetrieval) retrieveFallback(ctx context.Context, q Query) ([]MemoryItem, error) {
	if r.fallback == nil {
		return nil, nil
	}
	return r.fallback.Retrieve(ctx, q)
}
