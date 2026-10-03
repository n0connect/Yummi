package api

import "testing"

// The operator-facing terminal view must never be able to write to an agent
// session. These verbs are the only ones it may call.
func TestAgentTerminalVerbWhitelistIsReadOnly(t *testing.T) {
	for _, ok := range []string{"term_list", "term_read"} {
		if !allowedAgentTerminalVerb(ok) {
			t.Errorf("%s should be allowed (read verb)", ok)
		}
	}
	for _, denied := range []string{"term_run", "term_keys", "term_wait", "term_open", "term_close", "nmap_scan", "run_command"} {
		if allowedAgentTerminalVerb(denied) {
			t.Errorf("%s must NOT be reachable from the operator terminal view", denied)
		}
	}
}

func TestAgentTerminalCallRefusesWritesBeforeAnyTraffic(t *testing.T) {
	// nil client: a permitted verb would fail with "not configured", but a
	// write verb must be refused by the verb check first.
	if _, err := agentTerminalCall(t.Context(), nil, "term_keys", map[string]any{"id": "x", "keys": "C-c"}); err == nil {
		t.Fatal("term_keys must be refused")
	} else if got := err.Error(); got != `terminal view is read-only: "term_keys" is not permitted` {
		t.Fatalf("unexpected refusal: %q", got)
	}
}

func TestParseTermListRows(t *testing.T) {
	out := "listener  1w  200x50  Sat Oct  3 17:39:52 2026  label=listener\n" +
		"scan  1w  200x50  Sat Oct  3 17:44:01 2026\n"
	got := parseTermList(out)
	if len(got) != 2 {
		t.Fatalf("want 2 terminals, got %d: %+v", len(got), got)
	}
	if got[0].ID != "listener" || got[0].Label != "listener" || got[0].Size != "200x50" {
		t.Errorf("labelled row parsed wrong: %+v", got[0])
	}
	if got[1].ID != "scan" || got[1].Label != "" || got[1].Created != "Sat Oct 3 17:44:01 2026" {
		t.Errorf("unlabelled row parsed wrong: %+v", got[1])
	}
}

func TestParseTermListEmpty(t *testing.T) {
	got := parseTermList("no open sessions")
	if got == nil || len(got) != 0 {
		t.Errorf("expected a non-nil empty list, got %#v", got)
	}
}

func TestAgentTerminalIDPattern(t *testing.T) {
	for _, ok := range []string{"listener", "scan-1", "a_b"} {
		if !agentTerminalIDPattern.MatchString(ok) {
			t.Errorf("%q should be a valid id", ok)
		}
	}
	for _, bad := range []string{"", "a b", "x;rm", "../etc", "a/b"} {
		if agentTerminalIDPattern.MatchString(bad) {
			t.Errorf("%q must be rejected", bad)
		}
	}
}
