package knowledge

import (
	"context"
	"errors"
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

// TestListTacticsDeprioritizesTrivialHighHitCount proves a bare, always-
// succeed command (id) run 50 times does not outrank a specific,
// technique-carrying command (nmap with flags) run only 3 times — plain
// hit_count DESC used to let repetition alone dominate ranking.
func TestListTacticsDeprioritizesTrivialHighHitCount(t *testing.T) {
	s := memStore(t)
	ctx := context.Background()
	for range 50 {
		_ = s.RecordTactic(ctx, "id", "worked", nil, "")
	}
	for range 3 {
		_ = s.RecordTactic(ctx, "nmap -sV -p- --min-rate 1000 10.0.0.5", "worked", nil, "")
	}

	got := s.ListTactics("id", 10)
	if len(got) != 1 || got[0].HitCount != 50 {
		t.Fatalf("expected the id tactic to still be recorded with hit_count 50, got %+v", got)
	}

	idScore := tacticScore(got[0])
	nmapTactics := s.ListTactics("nmap", 10)
	if len(nmapTactics) != 1 {
		t.Fatalf("want 1 nmap tactic, got %+v", nmapTactics)
	}
	nmapScore := tacticScore(nmapTactics[0])
	if nmapScore <= idScore {
		t.Fatalf("expected nmap (complex, %d hits) to outscore id (trivial, 50 hits): nmap=%f id=%f",
			nmapTactics[0].HitCount, nmapScore, idScore)
	}
}

// TestListTacticsWorkedOutranksFailedAtEqualShape proves that, all else
// equal, a "worked" tactic ranks above a "failed" one.
func TestListTacticsWorkedOutranksFailedAtEqualShape(t *testing.T) {
	s := memStore(t)
	ctx := context.Background()
	_ = s.RecordTactic(ctx, "gobuster dir -u 10.0.0.5 -w big.txt", "failed", nil, "")
	_ = s.RecordTactic(ctx, "ffuf -u 10.0.0.9 -w big.txt", "worked", nil, "")

	got := s.ListTactics("gobuster", 10)
	if len(got) != 1 || got[0].Outcome != "failed" {
		t.Fatalf("unexpected gobuster tactic: %+v", got)
	}
	failedScore := tacticScore(got[0])

	worked := s.ListTactics("ffuf", 10)
	if len(worked) != 1 || worked[0].Outcome != "worked" {
		t.Fatalf("unexpected ffuf tactic: %+v", worked)
	}
	workedScore := tacticScore(worked[0])

	if workedScore <= failedScore {
		t.Fatalf("expected worked (%f) to outscore failed (%f) at equal hit_count/complexity", workedScore, failedScore)
	}
}

func TestRecordTacticEmbedsOnWriteWhenEmbedderConfigured(t *testing.T) {
	s := memStore(t)
	s.SetEmbedder(&fakeEmbedder{vec: map[string][]float32{"nmap <TARGET>": {1, 2, 3}}}, "m")

	ctx := context.Background()
	if err := s.RecordTactic(ctx, "nmap 10.0.0.5", "worked", nil, ""); err != nil {
		t.Fatalf("RecordTactic: %v", err)
	}
	got := s.ListTactics("nmap", 10)
	if len(got) != 1 {
		t.Fatalf("want 1 tactic, got %d", len(got))
	}
	vec, ok := s.getEmbedding("procedural_memory", got[0].ID, "m")
	if !ok {
		t.Fatal("expected an embedding to be saved for the new tactic")
	}
	if len(vec) != 3 {
		t.Fatalf("expected the fake embedder's 3-dim vector, got %v", vec)
	}
}

func TestRecordTacticWithoutEmbedderStillWorks(t *testing.T) {
	s := memStore(t) // no SetEmbedder call — must behave exactly as before
	if err := s.RecordTactic(context.Background(), "nmap 10.0.0.5", "worked", nil, ""); err != nil {
		t.Fatalf("RecordTactic without embedder: %v", err)
	}
	if len(s.ListTactics("nmap", 10)) != 1 {
		t.Fatal("expected the tactic to still be recorded with no embedder configured")
	}
}

func TestRecordTacticEmbedFailureIsBestEffort(t *testing.T) {
	s := memStore(t)
	s.SetEmbedder(&fakeEmbedder{err: errors.New("ollama down")}, "m")

	err := s.RecordTactic(context.Background(), "nmap 10.0.0.5", "worked", nil, "")
	if err != nil {
		t.Fatalf("expected RecordTactic to succeed despite embed failure (best-effort), got: %v", err)
	}
	if len(s.ListTactics("nmap", 10)) != 1 {
		t.Fatal("expected the tactic to still be recorded despite the embed failure")
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
