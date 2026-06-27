package agent

import (
	"strings"
	"testing"
)

func TestAddObservationCapsHugeOutput(t *testing.T) {
	m := &MemoryContext{TokenBudget: 6000}
	huge := strings.Repeat("A", maxRetainedObservation+10_000)
	m.AddObservation(huge)

	if len(m.Observations) != 1 {
		t.Fatalf("want 1 observation, got %d", len(m.Observations))
	}
	got := m.Observations[0]
	if len(got) > maxRetainedObservation+200 { // +marker overhead
		t.Fatalf("observation not capped: %d bytes", len(got))
	}
	if !strings.Contains(got, "observation capped") {
		t.Fatalf("capped observation should carry the marker")
	}
	// Head and tail of the original must survive.
	if !strings.HasPrefix(got, "AAAA") || !strings.HasSuffix(got, "AAAA") {
		t.Fatalf("head/tail not preserved")
	}
}

func TestAddObservationSmallOutputUntouched(t *testing.T) {
	m := &MemoryContext{TokenBudget: 6000}
	small := "22/tcp open ssh"
	m.AddObservation(small)
	if m.Observations[0] != small {
		t.Fatalf("small observation altered: %q", m.Observations[0])
	}
}
