package knowledge

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeEmbedder is a real, hand-written test double (not a mock framework) —
// consistent with the repo's "no mocks of Yummi's own components" rule; this
// satisfies an external-boundary interface (Embedder), not a Yummi type.
type fakeEmbedder struct {
	vec map[string][]float32
	err error
}

func (f *fakeEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	if f.err != nil {
		return nil, f.err
	}
	if v, ok := f.vec[text]; ok {
		return v, nil
	}
	return []float32{0, 0, 1}, nil
}

func TestSemanticRetrievalNilEmbedderDegradesToStructured(t *testing.T) {
	s := memStore(t)
	ctx := context.Background()
	_ = s.RecordTactic(ctx, "nmap -sV 10.0.0.5", "worked", nil, "")

	structured := NewStructuredRetrieval(s)
	sem := NewSemanticRetrieval(s, nil, "m", structured)

	q := Query{SessionID: "s", Tools: []string{"nmap"}, Limit: 5}
	semItems, err := sem.Retrieve(ctx, q)
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	structItems, _ := structured.Retrieve(ctx, q)
	if len(semItems) != len(structItems) {
		t.Fatalf("expected nil-embedder path to match structured output: sem=%+v struct=%+v", semItems, structItems)
	}
}

func TestSemanticRetrievalEmbedderErrorDegradesToStructured(t *testing.T) {
	s := memStore(t)
	ctx := context.Background()
	_ = s.RecordTactic(ctx, "nmap -sV 10.0.0.5", "worked", nil, "")

	structured := NewStructuredRetrieval(s)
	sem := NewSemanticRetrieval(s, &fakeEmbedder{err: errors.New("ollama unreachable")}, "m", structured)

	items, err := sem.Retrieve(ctx, Query{SessionID: "s", Tools: []string{"nmap"}, Limit: 5})
	if err != nil {
		t.Fatalf("expected graceful degrade, got error: %v", err)
	}
	var sawTactic bool
	for _, it := range items {
		if it.Kind == "tactic" {
			sawTactic = true
		}
	}
	if !sawTactic {
		t.Fatalf("expected fallback to structured retrieval to still surface the tactic, got %+v", items)
	}
}

func TestSemanticRetrievalCrossToolRanking(t *testing.T) {
	s := memStore(t)
	ctx := context.Background()

	_ = s.RecordTactic(ctx, "nmap -sV 10.0.0.5", "worked", nil, "")
	_ = s.RecordTactic(ctx, "masscan -p1-65535 10.0.0.5", "worked", nil, "")
	_ = s.RecordTactic(ctx, "whoami", "worked", nil, "") // unrelated, orthogonal vector

	nmapTacs := s.ListTactics("nmap", 10)
	massTacs := s.ListTactics("masscan", 10)
	whoTacs := s.ListTactics("whoami", 10)
	if err := s.SaveEmbedding(ctx, "procedural_memory", nmapTacs[0].ID, "m", []float32{1, 0, 0}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := s.SaveEmbedding(ctx, "procedural_memory", massTacs[0].ID, "m", []float32{0.9, 0.1, 0}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := s.SaveEmbedding(ctx, "procedural_memory", whoTacs[0].ID, "m", []float32{0, 0, 1}); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Querying with tool "nmap" must still surface the semantically-close
	// "masscan" tactic — the entire point of semantic over structured retrieval.
	embedder := &fakeEmbedder{vec: map[string][]float32{"nmap": {1, 0, 0}}}
	sem := NewSemanticRetrieval(s, embedder, "m", NewStructuredRetrieval(s))

	// Limit 2: only the top-2 most similar tactics survive. "whoami" is
	// orthogonal ({0,0,1} vs a {1,0,0} query) so it must be the one dropped.
	items, err := sem.Retrieve(ctx, Query{SessionID: "s", Tools: []string{"nmap"}, Limit: 2})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	var sawMasscan, sawWhoami bool
	for _, it := range items {
		if strings.Contains(it.Text, "masscan") {
			sawMasscan = true
		}
		if strings.Contains(it.Text, "whoami") {
			sawWhoami = true
		}
	}
	if !sawMasscan {
		t.Fatalf("expected cross-tool semantic match (masscan) to surface for an nmap query, got %+v", items)
	}
	if sawWhoami {
		t.Fatalf("expected the orthogonal (unrelated) tactic to rank below limit, got %+v", items)
	}
}

func TestSemanticRetrievalRespectsExcludeCommands(t *testing.T) {
	s := memStore(t)
	ctx := context.Background()
	_ = s.RecordTactic(ctx, "nmap -sV 10.0.0.5", "worked", nil, "")

	tacs := s.ListTactics("nmap", 10)
	if err := s.SaveEmbedding(ctx, "procedural_memory", tacs[0].ID, "m", []float32{1, 0}); err != nil {
		t.Fatalf("save: %v", err)
	}

	embedder := &fakeEmbedder{vec: map[string][]float32{"nmap": {1, 0}}}
	sem := NewSemanticRetrieval(s, embedder, "m", NewStructuredRetrieval(s))

	items, err := sem.Retrieve(ctx, Query{
		SessionID: "s", Tools: []string{"nmap"}, Limit: 5,
		ExcludeCommands: []string{"nmap -sV 10.0.0.9"}, // same scrubbed pattern, different target
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	for _, it := range items {
		if it.Kind == "tactic" {
			t.Fatalf("expected excluded-pattern tactic to be filtered out, got %+v", items)
		}
	}
}

func TestSemanticRetrievalUsesGoalAndRecallsContext(t *testing.T) {
	s := memStore(t)
	ctx := context.Background()
	embedder := &fakeEmbedder{vec: map[string][]float32{
		"inspect JWT validation":                  {1, 0},
		"auth-map JWT validation is in verify.go": {1, 0},
	}}
	s.SetEmbedder(embedder, "m")
	if err := s.RecordContext(ctx, PersistentContext{
		SessionID: "s", Key: "auth-map", Value: "JWT validation is in verify.go",
	}); err != nil {
		t.Fatalf("RecordContext: %v", err)
	}

	sem := NewSemanticRetrieval(s, embedder, "m", NewStructuredRetrieval(s))
	items, err := sem.Retrieve(ctx, Query{SessionID: "s", Goal: "inspect JWT validation", Limit: 5})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	for _, item := range items {
		if item.Kind == "context" && strings.Contains(item.Text, "verify.go") {
			return
		}
	}
	t.Fatalf("semantic retrieval did not recall goal-relevant context: %+v", items)
}
