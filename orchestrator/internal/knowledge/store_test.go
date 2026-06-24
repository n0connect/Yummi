package knowledge

import (
	"context"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func memStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(nil) // in-memory map path
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

func TestRecordAndListFinding(t *testing.T) {
	s := memStore(t)
	ctx := context.Background()
	f := Finding{
		SessionID: "sess1", TaskID: "task1", Severity: "high",
		Title: "open redis", Description: "no auth", Evidence: "PING/PONG",
		Source: "agent",
	}
	if err := s.RecordFinding(ctx, f); err != nil {
		t.Fatalf("RecordFinding: %v", err)
	}
	got := s.ListFindings("sess1")
	if len(got) != 1 {
		t.Fatalf("want 1 finding, got %d", len(got))
	}
	if got[0].Source != "agent" || got[0].Title != "open redis" {
		t.Fatalf("unexpected finding: %+v", got[0])
	}
	if got[0].ID == "" {
		t.Fatal("finding ID not assigned")
	}
}

func TestRecordFindingDefaultsSourceManual(t *testing.T) {
	s := memStore(t)
	_ = s.RecordFinding(context.Background(), Finding{SessionID: "s", Severity: "low", Title: "x"})
	got := s.ListFindings("s")
	if len(got) != 1 || got[0].Source != "manual" {
		t.Fatalf("want source defaulted to manual, got %+v", got)
	}
}

func TestCreateAndListTarget(t *testing.T) {
	s := memStore(t)
	if err := s.CreateTarget(context.Background(), Target{SessionID: "s", Value: "10.0.0.5", InScope: true}); err != nil {
		t.Fatalf("CreateTarget: %v", err)
	}
	got := s.ListTargets("s")
	if len(got) != 1 || got[0].Value != "10.0.0.5" || !got[0].InScope {
		t.Fatalf("unexpected targets: %+v", got)
	}
}
