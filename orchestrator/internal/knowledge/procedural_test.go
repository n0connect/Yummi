package knowledge

import (
	"context"
	"strings"
	"testing"
)

func TestRecordTacticScrubsTarget(t *testing.T) {
	s := memStore(t)
	err := s.RecordTactic(context.Background(), "nmap -sV 10.0.0.5", "worked", nil, "")
	if err != nil {
		t.Fatalf("RecordTactic: %v", err)
	}
	got := s.ListTactics("nmap", 10)
	if len(got) != 1 {
		t.Fatalf("want 1 tactic, got %d", len(got))
	}
	if strings.Contains(got[0].CommandPattern, "10.0.0.5") {
		t.Fatalf("target leaked into procedural memory: %q", got[0].CommandPattern)
	}
	if !strings.Contains(got[0].CommandPattern, "<TARGET>") {
		t.Fatalf("expected <TARGET> placeholder, got %q", got[0].CommandPattern)
	}
	if got[0].Tool != "nmap" || got[0].Outcome != "worked" {
		t.Fatalf("unexpected tactic: %+v", got[0])
	}
}

func TestRecordTacticDedupIncrementsHitCount(t *testing.T) {
	s := memStore(t)
	ctx := context.Background()
	_ = s.RecordTactic(ctx, "nmap -sV 10.0.0.5", "worked", nil, "")
	_ = s.RecordTactic(ctx, "nmap -sV 10.0.0.9", "worked", nil, "") // same shape after scrub
	got := s.ListTactics("nmap", 10)
	if len(got) != 1 {
		t.Fatalf("want 1 deduped tactic, got %d", len(got))
	}
	if got[0].HitCount != 2 {
		t.Fatalf("want hit_count 2, got %d", got[0].HitCount)
	}
}

func TestRecordTacticFailClosedWhenTargetUnremovable(t *testing.T) {
	s := memStore(t)
	// A command with an extractable host must have it scrubbed, never leaked.
	err := s.RecordTactic(context.Background(), "curl https://victim.example.internal/api", "worked", nil, "")
	if err != nil {
		t.Fatalf("RecordTactic: %v", err)
	}
	for _, tac := range s.ListTactics("curl", 10) {
		if strings.Contains(tac.CommandPattern, "victim.example.internal") {
			t.Fatalf("hostname leaked: %q", tac.CommandPattern)
		}
	}
}
