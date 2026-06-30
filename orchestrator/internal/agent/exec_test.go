package agent

import (
	"context"
	"testing"
	"time"

	"github.com/n0connect/yummi/orchestrator/internal/execution"
)

// TestStoreExecerStubCompletion verifies that StoreExecer correctly maps
// the execution store's built-in stub result to ToolResult.
// No processor is injected — the store's dry-run stub completes automatically.
func TestStoreExecerStubCompletion(t *testing.T) {
	es := execution.NewStore()
	e := NewStoreExecer(es, 5*time.Second)

	result, err := e.Run(context.Background(), "sess-1", "whoami")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.IsError {
		t.Fatal("expected non-error result from stub execution")
	}
	if result.Content == "" {
		t.Fatal("expected non-empty stub output in Content")
	}
	if result.Duration <= 0 {
		t.Fatal("expected positive duration")
	}
}

// TestStoreExecerEgressHold proves the handle reports an egress-approval hold
// (so node_tool can show an accurate "awaiting egress approval" prompt instead
// of a misleading "still running"). A processor returning the egress sentinel
// holds the execution; Wait returns not-done and EgressHold surfaces the target.
func TestStoreExecerEgressHold(t *testing.T) {
	es := execution.NewStore()
	es.SetProcessor(func(ctx context.Context, rec execution.Record) (string, error) {
		return "", execution.ErrEgressApprovalRequired
	})
	e := NewStoreExecer(es, 5*time.Second)

	handle, err := e.Start(context.Background(), "sess-1", "nmap -sV 8.8.8.8")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	// Short Wait: the command never completes (it's held), so done must be false.
	_, done := handle.Wait(500 * time.Millisecond)
	if done {
		t.Fatal("held execution should not report done")
	}
	target, held := handle.EgressHold()
	if !held {
		t.Fatal("expected EgressHold to report the command is held")
	}
	if target != "8.8.8.8" {
		t.Fatalf("EgressHold target = %q, want 8.8.8.8", target)
	}
}

// TestStoreExecerContextCancellation verifies that context cancellation
// is propagated as a Go error (not a ToolResult.IsError).
func TestStoreExecerContextCancellation(t *testing.T) {
	es := execution.NewStore()
	e := NewStoreExecer(es, 30*time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately before Run

	_, err := e.Run(ctx, "sess-1", "sleep 10")
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

// TestStoreExecerTimeout verifies that WaitForTerminal respects the timeout.
// The store has no processor and processDelay defaults to 300ms; we use a
// 1ms timeout to force a timeout error.
func TestStoreExecerTimeout(t *testing.T) {
	es := execution.NewStore()
	e := NewStoreExecer(es, 1*time.Millisecond) // far below 300ms stub delay

	_, err := e.Run(context.Background(), "sess-1", "slow-command")
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

// TestStoreExecer_StartWaitCompletes verifies the supervisable Start→Wait path
// reaches a terminal success via the store's built-in dry-run stub.
func TestStoreExecer_StartWaitCompletes(t *testing.T) {
	es := execution.NewStore()
	e := NewStoreExecer(es, 5*time.Second)

	h, err := e.Start(context.Background(), "sess", "whoami")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	res, done := h.Wait(5 * time.Second)
	if !done {
		t.Fatal("expected terminal within window")
	}
	if res.IsError || res.Content == "" {
		t.Fatalf("unexpected result: %+v", res)
	}
}

// TestStoreExecer_SoftTimeoutThenComplete verifies a too-short Wait reports
// not-done while the command is still running, and a subsequent Wait completes.
func TestStoreExecer_SoftTimeoutThenComplete(t *testing.T) {
	es := execution.NewStore()
	e := NewStoreExecer(es, 5*time.Second)

	h, err := e.Start(context.Background(), "sess", "slow")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, done := h.Wait(1 * time.Millisecond); done {
		t.Skip("stub completed faster than 1ms; cannot exercise soft timeout deterministically")
	}
	if _, done := h.Wait(5 * time.Second); !done {
		t.Fatal("expected completion on the second wait")
	}
}

// TestStoreExecer_Cancel verifies Cancel yields a terminal IsError result.
func TestStoreExecer_Cancel(t *testing.T) {
	es := execution.NewStore()
	e := NewStoreExecer(es, 5*time.Second)

	h, err := e.Start(context.Background(), "sess", "slow")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	h.Cancel()
	res, done := h.Wait(5 * time.Second)
	if !done {
		t.Fatal("expected terminal after cancel")
	}
	if !res.IsError {
		t.Fatalf("cancel should yield an IsError result: %+v", res)
	}
}

// TestStoreExecerIntegration exercises real Docker exec via the sandbox bridge.
// Requires the sandbox container to be running.
func TestStoreExecerIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires sandbox container (run without -short)")
	}
	t.Skip("integration test: run manually with sandbox container running")
}
