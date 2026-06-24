package knowledge

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// dbStore exercises the REAL SQLite path (db != nil), which is what production
// uses — the map-backed path in the other tests does not cover the SQL queries.
func dbStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// :memory: gives each connection its own DB unless we pin to one connection.
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	s, err := NewStore(db)
	if err != nil {
		t.Fatalf("NewStore(db): %v", err)
	}
	return s
}

func TestDBFindingRoundTrip(t *testing.T) {
	s := dbStore(t)
	ctx := context.Background()
	if err := s.RecordFinding(ctx, Finding{
		SessionID: "s1", TaskID: "t1", Severity: "high",
		Title: "open redis", Description: "no auth", Evidence: "PONG", Source: "agent",
	}); err != nil {
		t.Fatalf("RecordFinding: %v", err)
	}
	got := s.ListFindings("s1")
	if len(got) != 1 {
		t.Fatalf("want 1, got %d", len(got))
	}
	f := got[0]
	if f.Source != "agent" || f.TaskID != "t1" || f.Title != "open redis" || f.Severity != "high" || f.Evidence != "PONG" {
		t.Fatalf("column mapping wrong: %+v", f)
	}
}

func TestDBTargetRoundTrip(t *testing.T) {
	s := dbStore(t)
	if err := s.CreateTarget(context.Background(), Target{SessionID: "s1", Value: "10.0.0.5", InScope: true, Notes: "web"}); err != nil {
		t.Fatalf("CreateTarget: %v", err)
	}
	got := s.ListTargets("s1")
	if len(got) != 1 || got[0].Value != "10.0.0.5" || !got[0].InScope || got[0].Notes != "web" {
		t.Fatalf("target column mapping wrong: %+v", got)
	}
}

func TestDBTacticDedupViaSQL(t *testing.T) {
	s := dbStore(t)
	ctx := context.Background()
	if err := s.RecordTactic(ctx, "nmap -sV 10.0.0.5", "worked", []string{"recon"}, "fingerprint"); err != nil {
		t.Fatalf("RecordTactic 1: %v", err)
	}
	if err := s.RecordTactic(ctx, "nmap -sV 192.168.1.1", "worked", nil, ""); err != nil {
		t.Fatalf("RecordTactic 2: %v", err)
	}
	got := s.ListTactics("nmap", 10)
	if len(got) != 1 {
		t.Fatalf("want 1 deduped tactic, got %d: %+v", len(got), got)
	}
	if got[0].HitCount != 2 {
		t.Fatalf("want hit_count 2 via SQL UPDATE, got %d", got[0].HitCount)
	}
	if strings.Contains(got[0].CommandPattern, "10.0.0.5") || !strings.Contains(got[0].CommandPattern, "<TARGET>") {
		t.Fatalf("scrub failed in SQL path: %q", got[0].CommandPattern)
	}
	if len(got[0].ContextTags) != 1 || got[0].ContextTags[0] != "recon" {
		t.Fatalf("context_tags not round-tripped via SQL: %+v", got[0].ContextTags)
	}
}

func TestDBRetrievalRoundTrip(t *testing.T) {
	s := dbStore(t)
	ctx := context.Background()
	_ = s.RecordFinding(ctx, Finding{SessionID: "s1", Severity: "high", Title: "open redis", Source: "agent"})
	_ = s.RecordTactic(ctx, "nmap -sV 10.0.0.5", "worked", nil, "")

	items, err := NewStructuredRetrieval(s).Retrieve(ctx, Query{SessionID: "s1", Tools: []string{"nmap"}, Limit: 5})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	var sawFinding, sawTactic bool
	for _, it := range items {
		sawFinding = sawFinding || (it.Kind == "finding" && strings.Contains(it.Text, "open redis"))
		sawTactic = sawTactic || (it.Kind == "tactic" && strings.Contains(it.Text, "<TARGET>"))
	}
	if !sawFinding || !sawTactic {
		t.Fatalf("SQL retrieval incomplete: finding=%v tactic=%v", sawFinding, sawTactic)
	}
}
