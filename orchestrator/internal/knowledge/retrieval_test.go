package knowledge

import (
	"context"
	"database/sql"
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

func TestStructuredRetrievalRecallsPersistentContext(t *testing.T) {
	for _, db := range []*sql.DB{nil, openTestDB(t)} {
		s, err := NewStore(db)
		if err != nil {
			t.Fatalf("NewStore: %v", err)
		}
		ctx := context.Background()
		if err := s.RecordContext(ctx, PersistentContext{
			SessionID: "s", Key: "auth-module", Value: "JWT verification lives in internal/auth/verify.go",
		}); err != nil {
			t.Fatalf("RecordContext: %v", err)
		}
		if err := s.RecordContext(ctx, PersistentContext{
			SessionID: "other", Key: "secret", Value: "must never cross session boundaries",
		}); err != nil {
			t.Fatalf("RecordContext other: %v", err)
		}

		items, err := NewStructuredRetrieval(s).Retrieve(ctx, Query{
			SessionID: "s", Goal: "review authentication and JWT verification", Limit: 5,
		})
		if err != nil {
			t.Fatalf("Retrieve: %v", err)
		}
		var recalled bool
		for _, item := range items {
			if item.Kind == "context" && strings.Contains(item.Text, "verify.go") {
				recalled = true
			}
			if strings.Contains(item.Text, "cross session") {
				t.Fatalf("context leaked across sessions: %+v", items)
			}
		}
		if !recalled {
			t.Fatalf("persistent context was written but not recalled: %+v", items)
		}
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

// TestRetrieveExcludesCurrentTaskCommands is the regression guard for the agent
// command-loop bug: procedural memory must NOT echo back a command already run
// in the current task. Recalling "ps -ef (worked, used 3x)" right after running
// ps -ef — under the "use it, don't rediscover" framing — is what reinforced
// weak models into re-running near-identical recon commands in a loop.
func TestRetrieveExcludesCurrentTaskCommands(t *testing.T) {
	s := memStore(t)
	ctx := context.Background()
	_ = s.RecordTactic(ctx, "ps -ef", "worked", nil, "")
	_ = s.RecordTactic(ctx, "ps -ejH", "worked", nil, "")

	r := NewStructuredRetrieval(s)
	// The model just ran "ps -ef" this task; it must not be recalled.
	items, err := r.Retrieve(ctx, Query{
		Tools:           []string{"ps"},
		ExcludeCommands: []string{"ps -ef"},
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	for _, it := range items {
		if it.Kind == "tactic" && strings.Contains(it.Text, "ps -ef ") {
			t.Fatalf("current-task command 'ps -ef' was echoed back as memory: %q", it.Text)
		}
	}
	// A DIFFERENT tactic not yet run this task should still surface (memory must
	// keep its cross-task value; we only suppress the current working set).
	var sawOther bool
	for _, it := range items {
		if strings.Contains(it.Text, "ps -ejH") {
			sawOther = true
		}
	}
	if !sawOther {
		t.Fatalf("non-excluded tactic 'ps -ejH' should still be recalled, got %+v", items)
	}
}

// A target-bearing command is excluded by its SCRUBBED pattern, matching how
// tactics are stored (nmap 10.0.0.5 → "nmap <TARGET>").
func TestRetrieveExcludesScrubbedPattern(t *testing.T) {
	s := memStore(t)
	ctx := context.Background()
	_ = s.RecordTactic(ctx, "nmap -sV 10.0.0.5", "worked", nil, "")

	r := NewStructuredRetrieval(s)
	items, _ := r.Retrieve(ctx, Query{
		Tools:           []string{"nmap"},
		ExcludeCommands: []string{"nmap -sV 10.0.0.9"}, // different target, same pattern
	})
	for _, it := range items {
		if it.Kind == "tactic" {
			t.Fatalf("scrubbed pattern should have been excluded, got %q", it.Text)
		}
	}
}
