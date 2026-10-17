package eino

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/n0connect/yummi/orchestrator/internal/agent"
	"github.com/n0connect/yummi/orchestrator/internal/conductor"
)

// Exercise the real store and approval channel. Denial ends before execution,
// so no model, MCP substitute or shell implementation is needed.
func TestPentestGatePublishesExactArgumentsAndClearsOnDenial(t *testing.T) {
	store := openTestStore(t)
	task, err := store.CreateSubtask("operator-qc", "check browser", 4, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Transition(task.ID, agent.StatusPlanned, nil); err != nil {
		t.Fatal(err)
	}
	if err = store.Transition(task.ID, agent.StatusRunning, nil); err != nil {
		t.Fatal(err)
	}
	gate := agent.NewGate()
	deps := pentestToolDeps{taskID: task.ID, sessionID: task.SessionID,
		store: store, gate: gate, validator: conductor.NewValidator(),
		approvalMode: func() agent.ApprovalMode { return agent.ModeAskPermission }}
	done := make(chan string, 1)
	go func() {
		out, callErr := runPentestCall(context.Background(), deps, "browser_open",
			map[string]any{"url": "http://127.0.0.1:18888/browser-qa.html", "headless": true}, "inspect local QA page", "low")
		if callErr != nil {
			out = callErr.Error()
		}
		done <- out
	}()
	t.Cleanup(func() { gate.Clear(task.ID) })
	deadline := time.Now().Add(2 * time.Second)
	var snapshot agent.Task
	for time.Now().Before(deadline) {
		snapshot, _ = store.Get(task.ID)
		if snapshot.Status == agent.StatusAwaitingApproval {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if snapshot.PendingApprovalKind != "command" || snapshot.PendingMCPTool != "browser_open" || snapshot.PendingProposalID == "" {
		t.Fatalf("typed command gate not surfaced: %+v", snapshot)
	}
	var args map[string]any
	if json.Unmarshal([]byte(snapshot.PendingMCPArgs), &args) != nil || args["url"] != "http://127.0.0.1:18888/browser-qa.html" || args["headless"] != true {
		t.Fatalf("exact arguments lost: %q", snapshot.PendingMCPArgs)
	}
	if err = gate.Resolve(task.ID, snapshot.PendingProposalID, false, "test denial"); err != nil {
		t.Fatal(err)
	}
	select {
	case out := <-done:
		if !strings.HasPrefix(out, "APPROVAL_DENIED:") {
			t.Fatalf("unexpected result %q", out)
		}
	case <-time.After(time.Second):
		t.Fatal("gate did not resolve")
	}
	snapshot, _ = store.Get(task.ID)
	if snapshot.PendingApprovalKind != "" || snapshot.PendingMCPTool != "" || snapshot.PendingMCPArgs != "" {
		t.Fatalf("stale typed authorization: %+v", snapshot)
	}
}
