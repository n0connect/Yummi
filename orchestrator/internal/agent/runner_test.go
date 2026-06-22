package agent

import (
	"context"
	"testing"
	"time"

	"github.com/n0connect/yummi/orchestrator/internal/conductor"
)

// waitForStatus polls the store until the task reaches want or a terminal state.
func waitForStatus(t *testing.T, store *Store, id string, want TaskStatus, timeout time.Duration) Task {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		tk, ok := store.Get(id)
		if ok {
			if tk.Status == want {
				return tk
			}
			if tk.Status.Terminal() && tk.Status != want {
				t.Fatalf("task reached terminal %s (want %s); lastError=%q", tk.Status, want, tk.LastError)
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	tk, _ := store.Get(id)
	t.Fatalf("timeout waiting for %s; current=%s lastError=%q", want, tk.Status, tk.LastError)
	return Task{}
}

// TestRunnerE2E_PlanThenMultiStepFinish drives the full Runner lifecycle:
// CreateAndPlan (planning LLM call) → Start (async loop) → two real commands →
// FINISH, asserting plan steps are marked Done and findings propagate.
func TestRunnerE2E_PlanThenMultiStepFinish(t *testing.T) {
	store, _ := NewStore(openTestDB(t))
	es := realShellStore(t)
	ollama := newFakeOllama(t,
		// 1. planning call
		`{"steps":[{"id":"s1","description":"identify the current user"},{"id":"s2","description":"confirm shell works"}]}`,
		// 2. step → run whoami
		"[RUN_COMMAND]\n{\"command\":\"whoami\",\"step_id\":\"s1\",\"risk_level\":\"low\",\"findings\":[\"user identified\"]}",
		// 3. step → run echo
		"[RUN_COMMAND]\n{\"command\":\"echo ok\",\"step_id\":\"s2\",\"risk_level\":\"low\"}",
		// 4. finish
		"[FINISH]\n{\"summary\":\"recon complete\",\"findings\":[\"shell confirmed\"]}",
	)

	cfg := fastRetryCfg()
	runner := NewRunner(store, ollama, nil, NewStoreExecer(es, 5*time.Second), cfg)

	task, err := runner.CreateAndPlan(context.Background(), "test-session", "recon the host")
	if err != nil {
		t.Fatalf("CreateAndPlan: %v", err)
	}
	if task.Status != StatusPlanned {
		t.Fatalf("expected planned after CreateAndPlan, got %s", task.Status)
	}
	if len(task.Plan) != 2 {
		t.Fatalf("expected 2 plan steps, got %d", len(task.Plan))
	}

	if err := runner.Start(task.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}

	got := waitForStatus(t, store, task.ID, StatusCompleted, 5*time.Second)
	if got.Summary != "recon complete" {
		t.Fatalf("summary mismatch: %q", got.Summary)
	}
	if planStep(got.Plan, "s1").Status != StepDone || planStep(got.Plan, "s2").Status != StepDone {
		t.Fatalf("expected both steps Done, got s1=%s s2=%s",
			planStep(got.Plan, "s1").Status, planStep(got.Plan, "s2").Status)
	}
	if len(got.Findings) == 0 {
		t.Fatal("expected findings to propagate to the finished task")
	}
}

// TestRunnerE2E_ApprovalGate drives a medium-risk command through the gate:
// the loop pauses awaiting_approval, the operator approves (same call the API
// HandleApproveTask makes), and the command then runs to completion.
func TestRunnerE2E_ApprovalGate(t *testing.T) {
	store, _ := NewStore(openTestDB(t))
	es := realShellStore(t)
	// A real validator computes RequiresApproval from the model's risk_level.
	validator := conductor.NewValidator()
	ollama := newFakeOllama(t,
		`{"steps":[{"id":"s1","description":"run a scan that needs approval"}]}`,
		"[RUN_COMMAND]\n{\"command\":\"echo scanning\",\"step_id\":\"s1\",\"risk_level\":\"medium\"}",
		"[FINISH]\n{\"summary\":\"scan approved and done\"}",
	)

	cfg := fastRetryCfg()                // ApprovalTimeout 5s — plenty for the test to approve
	cfg.ApprovalMode = ModeAskPermission // gate every command on the operator
	runner := NewRunner(store, ollama, validator, NewStoreExecer(es, 5*time.Second), cfg)

	task, err := runner.CreateAndPlan(context.Background(), "test-session", "scan with approval")
	if err != nil {
		t.Fatalf("CreateAndPlan: %v", err)
	}
	if err := runner.Start(task.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}

	pending := waitForStatus(t, store, task.ID, StatusAwaitingApproval, 5*time.Second)
	if pending.PendingProposalID == "" || pending.PendingCommand != "echo scanning" {
		t.Fatalf("pending fields not set: id=%q cmd=%q", pending.PendingProposalID, pending.PendingCommand)
	}
	if pending.PendingRisk != "medium" {
		t.Fatalf("expected medium risk gate, got %q", pending.PendingRisk)
	}

	// Operator approves (identical to the API's HandleApproveTask path).
	if err := runner.Approve(task.ID, pending.PendingProposalID, true, ""); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	got := waitForStatus(t, store, task.ID, StatusCompleted, 5*time.Second)
	if got.Summary != "scan approved and done" {
		t.Fatalf("summary mismatch: %q", got.Summary)
	}
}
