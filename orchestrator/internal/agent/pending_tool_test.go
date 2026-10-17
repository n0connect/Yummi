package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/n0connect/yummi/orchestrator/internal/conductor"
)

func TestGraphGatePublishesExactPendingMCPArguments(t *testing.T) {
	for _, kind := range []string{"command", "threat"} {
		t.Run(kind, func(t *testing.T) { testGraphGatePendingMCP(t, kind) })
	}
}

func testGraphGatePendingMCP(t *testing.T, kind string) {
	store, err := NewStore(openTestDB(t))
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.Create("operator-qc", "goal", 4)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Transition(task.ID, StatusPlanned, nil); err != nil {
		t.Fatal(err)
	}
	if err = store.Transition(task.ID, StatusRunning, nil); err != nil {
		t.Fatal(err)
	}
	gate := NewGate()
	s := &GraphState{taskID: task.ID, store: store, gate: gate, cfg: Config{ApprovalTimeout: time.Second}, pendingAct: Action{Tool: "browser_open", Args: map[string]any{"url": "http://127.0.0.1:18888/browser-qa.html", "headless": true}}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, err, _ := awaitGateVerdict(ctx, s, kind, "browser open local", conductor.Proposal{ID: "pending-exact", RiskLevel: "low"}, "timeout")
		done <- err
	}()
	var snapshot Task
	for {
		snapshot, _ = store.Get(task.ID)
		if snapshot.Status == StatusAwaitingApproval {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("gate never surfaced")
		case <-time.After(time.Millisecond):
		}
	}
	data, _ := json.Marshal(snapshot)
	var wire map[string]any
	_ = json.Unmarshal(data, &wire)
	if wire["pending_approval_kind"] != kind {
		t.Errorf("typed pending approval kind missing: %s", data)
	}
	if kind == "command" {
		if wire["pending_mcp_tool"] != "browser_open" {
			t.Errorf("typed pending tool missing: %s", data)
		}
		args, _ := wire["pending_mcp_args"].(string)
		var actual map[string]any
		if json.Unmarshal([]byte(args), &actual) != nil || actual["url"] != "http://127.0.0.1:18888/browser-qa.html" || actual["headless"] != true {
			t.Errorf("exact pending arguments missing: %s", data)
		}
	} else if wire["pending_mcp_tool"] != nil || wire["pending_mcp_args"] != nil {
		t.Errorf("threat hold must not publish command authorization: %s", data)
	}
	if err = gate.Resolve(task.ID, "pending-exact", false, "test denial"); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	snapshot, _ = store.Get(task.ID)
	data, _ = json.Marshal(snapshot)
	wire = map[string]any{}
	_ = json.Unmarshal(data, &wire)
	if wire["pending_mcp_tool"] != nil || wire["pending_mcp_args"] != nil || wire["pending_approval_kind"] != nil {
		t.Fatalf("stale pending metadata after resolution: %s", data)
	}
}
