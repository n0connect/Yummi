package tui

import (
	"testing"
	"time"
)

func TestNextBackoffEscalatesAndCaps(t *testing.T) {
	if got := nextBackoff(baseBackoff); got != baseBackoff*2 {
		t.Fatalf("want doubling, got %v", got)
	}
	// Escalate past the cap and confirm it clamps.
	cur := baseBackoff
	for i := 0; i < 20; i++ {
		cur = nextBackoff(cur)
	}
	if cur != maxBackoff {
		t.Fatalf("backoff should clamp to %v, got %v", maxBackoff, cur)
	}
}

func TestJitteredWithinTwentyPercent(t *testing.T) {
	d := 10 * time.Second
	for i := 0; i < 1000; i++ {
		j := jittered(d)
		if j < d-d/5 || j > d+d/5 {
			t.Fatalf("jitter %v outside ±20%% of %v", j, d)
		}
	}
	if jittered(0) != 0 {
		t.Fatalf("jittered(0) must be 0")
	}
}
