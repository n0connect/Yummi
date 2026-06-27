package agent

import (
	"context"
	"strings"
	"testing"
)

// TestGraph_ToolThrash_SemanticDuplicateLoop reproduces the reported bug: a weak
// model rotates trivially-different invocations of the SAME tool (the real report
// was ps -ef / ps -ejH / ps aux / ps -e). Each string is distinct, so exact-hash
// duplicate and stuck detection (which need an exact repeat or an A→B→A→B cycle)
// never fire — the model would loop indefinitely. The tool-thrash guard must
// catch it: nudge the model after the 4th same-tool command (NOT executing it),
// after which the model recovers and finishes.
//
// The test uses `echo` as the rotating tool instead of `ps`: the guard keys on
// the tool TOKEN, not the tool's identity, and real `ps -ef` output on the test
// host is large enough to trip summarization and desync the scripted LLM (the
// existing graph tests use tiny commands for the same reason). The logic under
// test — 4 distinct invocations of one tool token → nudge — is identical.
func TestGraph_ToolThrash_SemanticDuplicateLoop(t *testing.T) {
	store, _ := NewStore(openTestDB(t))
	task := newRunningTask(t, store, "list users and their processes")
	es := realShellStore(t)
	// Four DISTINCT same-tool invocations (distinct hashes → not duplicate; a,b,c,d
	// → not an A→B→A→B cycle, so not stuck), then recovery with a different tool.
	ollama := newFakeOllama(t,
		"[RUN_COMMAND]\n{\"command\":\"echo a\",\"step_id\":\"s1\",\"risk_level\":\"low\"}",
		"[RUN_COMMAND]\n{\"command\":\"echo b\",\"step_id\":\"s1\",\"risk_level\":\"low\"}",
		"[RUN_COMMAND]\n{\"command\":\"echo c\",\"step_id\":\"s1\",\"risk_level\":\"low\"}",
		"[RUN_COMMAND]\n{\"command\":\"echo d\",\"step_id\":\"s1\",\"risk_level\":\"low\"}", // 4th echo → thrash nudge, NOT executed
		"[RUN_COMMAND]\n{\"command\":\"true\",\"step_id\":\"s1\",\"risk_level\":\"low\"}", // recovery: different tool
		"[FINISH]\n{\"summary\":\"enumerated processes\"}")

	s := newToolState(t, store, task.ID, ollama, es, fastRetryCfg())
	if err := Execute(context.Background(), s, nodeStep); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got, _ := store.Get(task.ID)
	if got.Status != StatusCompleted {
		t.Fatalf("expected completion after thrash recovery, got %s (err %q)", got.Status, got.LastError)
	}

	// The thrash nudge must have been surfaced, naming the offending tool.
	sawThrashNudge := false
	for _, o := range s.memCtx.Observations {
		if strings.Contains(o, "[ABNORMAL_PATTERN]") && strings.Contains(o, "echo") {
			sawThrashNudge = true
			break
		}
	}
	if !sawThrashNudge {
		t.Fatalf("expected an [ABNORMAL_PATTERN] tool-thrash nudge naming echo; observations:\n%v", s.memCtx.Observations)
	}

	// The 4th echo (echo d) must NOT have executed — exactly 3 echo commands ran.
	echoRuns := 0
	for _, r := range es.ListBySession("test-session", 100) {
		if strings.HasPrefix(strings.TrimSpace(r.Command), "echo ") {
			echoRuns++
		}
	}
	if echoRuns != 3 {
		t.Fatalf("expected exactly 3 echo executions (4th thrash-blocked), got %d", echoRuns)
	}

	// The recovery command (different tool) must have actually run.
	sawRecovery := false
	for _, r := range es.ListBySession("test-session", 100) {
		if strings.TrimSpace(r.Command) == "true" {
			sawRecovery = true
		}
	}
	if !sawRecovery {
		t.Fatal("recovery command 'true' never executed")
	}
}

// TestGraph_LegitimateEscalation_NotBlocked guards against over-correction: three
// distinct nmap phases on the same target is a valid escalation, not thrash, and
// must run without a nudge interrupting it.
func TestGraph_LegitimateEscalation_NotBlocked(t *testing.T) {
	store, _ := NewStore(openTestDB(t))
	task := newRunningTask(t, store, "scan the target")
	es := realShellStore(t)
	// Use echo as a stand-in tool so the real shell succeeds; the point is three
	// distinct invocations of the SAME tool (count 3 < threshold 4) are allowed.
	ollama := newFakeOllama(t,
		"[RUN_COMMAND]\n{\"command\":\"echo phase1\",\"step_id\":\"s1\",\"risk_level\":\"low\"}",
		"[RUN_COMMAND]\n{\"command\":\"echo phase2\",\"step_id\":\"s1\",\"risk_level\":\"low\"}",
		"[RUN_COMMAND]\n{\"command\":\"echo phase3\",\"step_id\":\"s1\",\"risk_level\":\"low\"}",
		"[FINISH]\n{\"summary\":\"three phases done\"}")

	s := newToolState(t, store, task.ID, ollama, es, fastRetryCfg())
	if err := Execute(context.Background(), s, nodeStep); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got, _ := store.Get(task.ID)
	if got.Status != StatusCompleted {
		t.Fatalf("legitimate 3-phase escalation should complete, got %s", got.Status)
	}
	for _, o := range s.memCtx.Observations {
		if strings.Contains(o, "[ABNORMAL_PATTERN]") {
			t.Fatalf("3 distinct same-tool commands must not trigger a thrash nudge:\n%v", s.memCtx.Observations)
		}
	}
	// All three phases must have executed.
	runs := 0
	for _, r := range es.ListBySession("test-session", 100) {
		if strings.HasPrefix(strings.TrimSpace(r.Command), "echo phase") {
			runs++
		}
	}
	if runs != 3 {
		t.Fatalf("expected all 3 phases to run, got %d", runs)
	}
}
