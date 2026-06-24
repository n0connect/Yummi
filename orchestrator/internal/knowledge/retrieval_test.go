package knowledge

import (
	"context"
	"strings"
	"testing"
)

func TestStructuredRetrievalReturnsFactsAndTactics(t *testing.T) {
	s := memStore(t)
	ctx := context.Background()
	_ = s.RecordFinding(ctx, Finding{SessionID: "s", Severity: "high", Title: "open redis", Source: "agent"})
	_ = s.RecordTactic(ctx, "nmap -sV 10.0.0.5", "worked", nil, "")

	r := NewStructuredRetrieval(s)
	items, err := r.Retrieve(ctx, Query{SessionID: "s", Tools: []string{"nmap"}, Limit: 5})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	var sawFinding, sawTactic bool
	for _, it := range items {
		if it.Kind == "finding" && strings.Contains(it.Text, "open redis") {
			sawFinding = true
		}
		if it.Kind == "tactic" && strings.Contains(it.Text, "<TARGET>") {
			sawTactic = true
		}
	}
	if !sawFinding || !sawTactic {
		t.Fatalf("missing memory items: finding=%v tactic=%v items=%+v", sawFinding, sawTactic, items)
	}
}

func TestStructuredRetrievalEmptyWhenNothingStored(t *testing.T) {
	r := NewStructuredRetrieval(memStore(t))
	items, err := r.Retrieve(context.Background(), Query{SessionID: "s", Tools: []string{"nmap"}})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("want no items, got %+v", items)
	}
}
