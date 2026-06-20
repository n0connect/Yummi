package agent

import "testing"

func TestStatusTerminal(t *testing.T) {
	cases := []struct {
		status   TaskStatus
		terminal bool
	}{
		{StatusPlanning, false},
		{StatusPlanned, false},
		{StatusRunning, false},
		{StatusAwaitingApproval, false},
		{StatusPaused, false},
		{StatusCompleted, true},
		{StatusFailed, true},
		{StatusStopped, true},
		{StatusInterrupted, true},
	}
	for _, c := range cases {
		if got := c.status.Terminal(); got != c.terminal {
			t.Errorf("Terminal(%s) = %v, want %v", c.status, got, c.terminal)
		}
	}
}

func TestValidTransitions(t *testing.T) {
	cases := []struct {
		from, to TaskStatus
		ok       bool
	}{
		{StatusPlanning, StatusPlanned, true},
		{StatusPlanning, StatusFailed, true},
		{StatusPlanned, StatusRunning, true},
		{StatusRunning, StatusAwaitingApproval, true},
		{StatusAwaitingApproval, StatusRunning, true},
		{StatusRunning, StatusCompleted, true},
		{StatusRunning, StatusPaused, true},
		{StatusPaused, StatusRunning, true},
		{StatusRunning, StatusStopped, true},
		{StatusAwaitingApproval, StatusStopped, true},
		{StatusCompleted, StatusRunning, false}, // terminal states are final
		{StatusPlanned, StatusCompleted, false}, // must run first
		{StatusPlanning, StatusRunning, false},  // must be planned first
	}
	for _, c := range cases {
		if got := ValidTransition(c.from, c.to); got != c.ok {
			t.Errorf("ValidTransition(%s → %s) = %v, want %v", c.from, c.to, got, c.ok)
		}
	}
}
