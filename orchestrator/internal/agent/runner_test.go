package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/n0connect/yummi/orchestrator/internal/conductor"
	"github.com/n0connect/yummi/orchestrator/internal/llm"
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

// TestSetApprovalModePropagatesToSubAgents proves the operator-facing mode
// toggle (POST /api/v1/agent/mode -> Runner.SetApprovalMode) actually reaches
// every configured sub-agent Runner, not just the top-level one. Before this
// fix, sub-agents were built with an independent Config{} that always
// defaulted to ModeAskPermission and never changed — so there was no way to
// switch a sub-agent to auto at all, and GET /api/v1/agent/mode (which only
// ever reads the top-level runner) could misreport the operator's actual
// intent for delegated work.
func TestSetApprovalModePropagatesToSubAgents(t *testing.T) {
	web := NewRunner(nil, nil, nil, nil, Config{})
	recon := NewRunner(nil, nil, nil, nil, Config{})
	top := NewRunner(nil, nil, nil, nil, Config{
		SubAgents: map[string]*Runner{"web": web, "recon": recon},
	})

	if top.ApprovalMode() != ModeAskPermission || web.ApprovalMode() != ModeAskPermission || recon.ApprovalMode() != ModeAskPermission {
		t.Fatalf("expected all runners to default to ask_permission, got top=%s web=%s recon=%s",
			top.ApprovalMode(), web.ApprovalMode(), recon.ApprovalMode())
	}

	if err := top.SetApprovalMode(ModeAuto); err != nil {
		t.Fatalf("SetApprovalMode: %v", err)
	}

	if top.ApprovalMode() != ModeAuto {
		t.Fatalf("top-level runner mode = %s, want auto", top.ApprovalMode())
	}
	if web.ApprovalMode() != ModeAuto {
		t.Fatalf("web sub-agent mode = %s, want auto (mode toggle must propagate to sub-agents)", web.ApprovalMode())
	}
	if recon.ApprovalMode() != ModeAuto {
		t.Fatalf("recon sub-agent mode = %s, want auto (mode toggle must propagate to sub-agents)", recon.ApprovalMode())
	}

	// And back — confirm it's a live toggle, not a one-way default override.
	if err := top.SetApprovalMode(ModeAskPermission); err != nil {
		t.Fatalf("SetApprovalMode: %v", err)
	}
	if web.ApprovalMode() != ModeAskPermission || recon.ApprovalMode() != ModeAskPermission {
		t.Fatalf("expected sub-agents back to ask_permission, got web=%s recon=%s", web.ApprovalMode(), recon.ApprovalMode())
	}
}

// TestBuildStateConcurrentWithSetApprovalModeIsRaceFree is a regression test
// for a data race: buildState used to read r.cfg directly (a struct copy)
// with no lock, while SetApprovalMode writes r.cfg.ApprovalMode under r.mu —
// concurrent calls (a task starting while an operator flips the mode) would
// trip `go test -race`. Not a security bypass (the in-flight task keeps
// whatever mode it captures, which matches the intended "mode change applies
// to new tasks only" semantics either way) but a real, fixable race.
func TestBuildStateConcurrentWithSetApprovalModeIsRaceFree(t *testing.T) {
	store, _ := NewStore(openTestDB(t))
	r := NewRunner(store, nil, nil, nil, Config{})

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = r.SetApprovalMode(ModeAuto)
		}()
	}
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = r.buildState("nonexistent-task-id")
		}()
	}
	wg.Wait()
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

	if err := runner.Start(task.ID, ""); err != nil {
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
	if err := runner.Start(task.ID, ""); err != nil {
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

// TestResumeAfterAskOperatorInjectsMessageAndContinues is a regression test
// for a bug found via live stress-testing: once a task paused via
// [ASK_OPERATOR], resuming it (POST /api/v1/tasks/{id}/start) just re-entered
// the SAME "ask" checkpoint node, which immediately re-paused with a generic
// "The agent needs operator input to continue." message — the operator's
// actual answer had no way to reach the model at all, so ask_operator was a
// dead end in practice: Resume was a no-op that always re-paused forever.
// Start must (1) never resume INTO the terminal "ask" node — it should
// continue from the normal step node instead — and (2) thread an optional
// operator message in as the next observation so the model actually sees the
// answer to what it asked.
func TestResumeAfterAskOperatorInjectsMessageAndContinues(t *testing.T) {
	store, _ := NewStore(openTestDB(t))
	es := realShellStore(t)

	var mu sync.Mutex
	var capturedPrompts []string
	idx := 0
	responses := []string{
		`{"steps":[{"id":"s1","description":"do the thing"}]}`,
		"[ASK_OPERATOR]\n{\"question\":\"which host should I target?\"}",
		"[FINISH]\n{\"summary\":\"done after guidance\"}",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		capturedPrompts = append(capturedPrompts, string(body))
		content := responses[idx]
		if idx < len(responses)-1 {
			idx++
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		cj, _ := json.Marshal(content)
		fmt.Fprintf(w, `{"message":{"role":"assistant","content":%s},"done":true}`, cj)
	}))
	t.Cleanup(srv.Close)
	ollama := llm.NewClient(srv.URL, "test-model")

	cfg := fastRetryCfg()
	runner := NewRunner(store, ollama, nil, NewStoreExecer(es, 5*time.Second), cfg)

	task, err := runner.CreateAndPlan(context.Background(), "sess-resume", "goal needing guidance")
	if err != nil {
		t.Fatalf("CreateAndPlan: %v", err)
	}
	if err := runner.Start(task.ID, ""); err != nil {
		t.Fatalf("Start: %v", err)
	}

	paused := waitForStatus(t, store, task.ID, StatusPaused, 5*time.Second)
	if paused.Question != "which host should I target?" {
		t.Fatalf("expected the model's actual question, got %q", paused.Question)
	}

	if err := runner.Start(task.ID, "hedef 10.0.0.5, onu tara"); err != nil {
		t.Fatalf("Start (resume with guidance): %v", err)
	}

	got := waitForStatus(t, store, task.ID, StatusCompleted, 5*time.Second)
	if got.Summary != "done after guidance" {
		t.Fatalf("expected the task to actually continue and finish, got summary=%q status=%s", got.Summary, got.Status)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(capturedPrompts) < 3 {
		t.Fatalf("expected at least 3 LLM calls (plan, ask, post-resume), got %d", len(capturedPrompts))
	}
	if !strings.Contains(capturedPrompts[2], "10.0.0.5") {
		t.Fatalf("operator's resume message never reached the model's prompt:\n%s", capturedPrompts[2])
	}
}
