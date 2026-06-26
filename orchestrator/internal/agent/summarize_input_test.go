package agent

import (
	"strings"
	"testing"
)

func TestSummarizeInputFitsVerbatim(t *testing.T) {
	obs := []string{"a", "b", "c"}
	got := summarizeInput(obs, 8000)
	if got != "a\n---\nb\n---\nc" {
		t.Fatalf("small history should pass through verbatim, got %q", got)
	}
}

func TestSummarizeInputPreservesFirstAndTail(t *testing.T) {
	first := "FIRST: target 10.0.0.5 open 22,80,443"
	mid := strings.Repeat("X", 9000) // forces compression
	last := "LAST: found admin panel at /admin"
	obs := []string{first, mid, last}

	got := summarizeInput(obs, 4000)
	if len(got) > 4200 { // budget + small markers
		t.Fatalf("compressed input exceeds budget: %d", len(got))
	}
	if !strings.Contains(got, "FIRST: target 10.0.0.5") {
		t.Fatalf("first observation (grounding) was dropped:\n%s", got[:200])
	}
	if !strings.Contains(got, "LAST: found admin panel") {
		t.Fatalf("most recent observation was dropped")
	}
	if !strings.Contains(got, "omitted") {
		t.Fatalf("middle-omitted marker missing")
	}
}
