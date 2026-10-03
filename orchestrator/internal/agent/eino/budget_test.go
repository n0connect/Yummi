package eino

import (
	"errors"
	"strings"
	"testing"
)

func TestParseBudgetAnswer(t *testing.T) {
	cases := []struct {
		in    string
		extra int
		stop  bool
	}{
		{"stop", 0, true},
		{"Dur, yeter", 0, true},
		{"15", 15, false},
		{"devam et 40", 30, false}, // clamped to per-decision max
		{"", budgetDefaultGrant, false},
		{"continue please", budgetDefaultGrant, false},
	}
	for _, c := range cases {
		extra, stop := parseBudgetAnswer(c.in)
		if extra != c.extra || stop != c.stop {
			t.Errorf("parseBudgetAnswer(%q) = (%d,%v), want (%d,%v)", c.in, extra, stop, c.extra, c.stop)
		}
	}
}

func TestGrantBudgetCapsAtTotal(t *testing.T) {
	if got := grantBudget(15, 10); got != 25 {
		t.Errorf("grantBudget(15,10)=%d want 25", got)
	}
	if got := grantBudget(110, 30); got != budgetMaxTotal {
		t.Errorf("grantBudget(110,30)=%d want %d", got, budgetMaxTotal)
	}
}

func TestIsBudgetExhausted(t *testing.T) {
	if !isBudgetExhausted(errors.New("NodeRunError: exceeds max iterations")) {
		t.Error("expected budget error to be recognised")
	}
	if isBudgetExhausted(errors.New("connection refused")) {
		t.Error("unrelated error must not be treated as budget exhaustion")
	}
}

func TestBudgetPauseQuestionOffersGrantOrStop(t *testing.T) {
	q := budgetPauseQuestion(15)
	for _, want := range []string{budgetPausePrefix, "15 steps", "stop"} {
		if !strings.Contains(q, want) {
			t.Errorf("pause question missing %q:\n%s", want, q)
		}
	}
}
