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
	"github.com/n0connect/yummi/orchestrator/internal/skill"
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

func TestResumeRecoveredCompletesFromDurableCheckpoint(t *testing.T) {
	db := openTestDB(t)
	before, _ := NewStore(db)
	task, _ := before.Create("recover-session", "finish recovered work", 10)
	_ = before.Transition(task.ID, StatusPlanned, func(tk *Task) {
		tk.Plan = []PlanStep{{ID: "s1", Description: "verify state", Status: StepPending}}
	})
	_ = before.Update(task.ID, func(tk *Task) {
		tk.Checkpoint = Checkpoint{
			TaskID: task.ID, CurrentNode: nodeTool, LastObservation: Wrap("TOOL_RESPONSE", "prior evidence"),
			RecentCommands: []string{"nmap -sV 127.0.0.1"}, Summary: "prior summary",
		}
	})
	_ = before.Transition(task.ID, StatusRunning, nil)

	after, err := NewStore(db)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	recovered, _ := after.Get(task.ID)
	if recovered.Status != StatusPaused || !recovered.Checkpoint.ResumeAfterRestart {
		t.Fatalf("task not marked recoverable: %+v", recovered)
	}

	model := newFakeOllama(t, `[FINISH]
{"summary":"recovered and completed"}`)
	runner := NewRunner(after, model, nil, nil, fastRetryCfg())
	count, err := runner.ResumeRecovered()
	if err != nil || count != 1 {
		t.Fatalf("ResumeRecovered count=%d err=%v", count, err)
	}
	final := waitForStatus(t, after, task.ID, StatusCompleted, 5*time.Second)
	if final.Summary != "recovered and completed" || final.Checkpoint.ResumeAfterRestart {
		t.Fatalf("recovered task did not complete cleanly: %+v", final)
	}
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

func TestSelectedPlanStepLimitUsesStrictestReviewedCapability(t *testing.T) {
	selected := []skill.Skill{
		{Name: "general", PlanStepLimit: 0},
		{Name: "bounded", PlanStepLimit: 3},
		{Name: "atomic", PlanStepLimit: 1},
	}
	if got := selectedPlanStepLimit(selected); got != 1 {
		t.Fatalf("selectedPlanStepLimit=%d, want 1", got)
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
	if !strings.Contains(got.Summary, "recon complete") || !strings.Contains(got.Summary, "EVIDENCE DIGEST") {
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
	if !strings.Contains(got.Summary, "scan approved and done") || !strings.Contains(got.Summary, "EVIDENCE DIGEST") {
		t.Fatalf("summary mismatch: %q", got.Summary)
	}
}

// TestRunnerE2E_AutoModeStillGatesOnDestructiveCommand proves the one hard
// exception to "auto mode never gates": a command matching validator.go's
// destructiveFloor (rm -rf here) still pauses awaiting_approval even in
// ModeAuto, despite the model declaring it "low" risk — closing the gap
// where a small/misjudging model could otherwise run an irreversible
// destructive command completely unattended.
func TestRunnerE2E_AutoModeStillGatesOnDestructiveCommand(t *testing.T) {
	store, _ := NewStore(openTestDB(t))
	es := realShellStore(t)
	validator := conductor.NewValidator()
	ollama := newFakeOllama(t,
		`{"steps":[{"id":"s1","description":"clean up a directory"}]}`,
		"[RUN_COMMAND]\n{\"command\":\"rm -rf /tmp/yummi-test-destructive\",\"step_id\":\"s1\",\"risk_level\":\"low\"}",
		"[FINISH]\n{\"summary\":\"cleanup approved and done\"}",
	)

	cfg := fastRetryCfg()       // ModeAuto by default
	cfg.ApprovalMode = ModeAuto // explicit: this is exactly what auto mode should NOT bypass
	runner := NewRunner(store, ollama, validator, NewStoreExecer(es, 5*time.Second), cfg)

	task, err := runner.CreateAndPlan(context.Background(), "test-session", "clean up temp directory")
	if err != nil {
		t.Fatalf("CreateAndPlan: %v", err)
	}
	if err := runner.Start(task.ID, ""); err != nil {
		t.Fatalf("Start: %v", err)
	}

	pending := waitForStatus(t, store, task.ID, StatusAwaitingApproval, 5*time.Second)
	if pending.PendingCommand != "rm -rf /tmp/yummi-test-destructive" {
		t.Fatalf("expected the destructive command pending approval, got %q", pending.PendingCommand)
	}
	if pending.PendingRisk != "high" {
		t.Fatalf("expected destructiveFloor to escalate risk to high despite model saying low, got %q", pending.PendingRisk)
	}

	if err := runner.Approve(task.ID, pending.PendingProposalID, true, ""); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	got := waitForStatus(t, store, task.ID, StatusCompleted, 5*time.Second)
	if !strings.Contains(got.Summary, "cleanup approved and done") {
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

// TestCreateAndPlan_EnumeratedGoalDeterministicallyDecomposed proves the
// under-decomposition guard: even when the planner LLM collapses an
// explicitly 3-part goal into ONE step (the live-confirmed failure — the
// model re-collapses it no matter how it is asked), the guard splits the
// operator's own enumeration markers in code into one plan step per part.
func TestCreateAndPlan_EnumeratedGoalDeterministicallyDecomposed(t *testing.T) {
	store, err := NewStore(nil)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	// The model stubbornly returns a single step, regardless of the goal.
	oneStep := `{"steps":[{"id":"s1","description":"find largest files in /var"}]}`
	r := NewRunner(store, newFakeOllama(t, oneStep), nil, nil, Config{Persona: testPersona, StepBudget: 15})

	goal := "Uc sey yap: 1) /var altinda en buyuk 3 dosya, 2) CPU cekirdek sayisi, 3) /etc/passwd kullanicilari"
	task, err := r.CreateAndPlan(context.Background(), "sess", goal)
	if err != nil {
		t.Fatalf("CreateAndPlan: %v", err)
	}
	if len(task.Plan) != 3 {
		t.Fatalf("plan has %d steps, want 3 (deterministic split of a 3-part goal): %+v", len(task.Plan), task.Plan)
	}
	// Each step must carry the operator's own text for that part.
	wants := []string{"/var altinda en buyuk 3 dosya", "CPU cekirdek sayisi", "/etc/passwd kullanicilari"}
	for i, w := range wants {
		if task.Plan[i].Description != w {
			t.Errorf("step %d description = %q, want %q", i+1, task.Plan[i].Description, w)
		}
	}
}

// TestCreateAndPlan_FreeFormGoalNotTouched is the negative control: a single
// free-form instruction (no enumeration markers) must keep exactly the plan
// the model produced — the deterministic split must never fire on it.
func TestCreateAndPlan_FreeFormGoalNotTouched(t *testing.T) {
	store, _ := NewStore(nil)
	oneStep := `{"steps":[{"id":"s1","description":"find the largest file under /var"}]}`
	r := NewRunner(store, newFakeOllama(t, oneStep), nil, nil, Config{Persona: testPersona, StepBudget: 15})

	task, err := r.CreateAndPlan(context.Background(), "sess", "find the largest file under /var and report its size")
	if err != nil {
		t.Fatalf("CreateAndPlan: %v", err)
	}
	if len(task.Plan) != 1 {
		t.Fatalf("plan has %d steps, want 1 — split must not fire on a free-form goal: %+v", len(task.Plan), task.Plan)
	}
}

// TestCreateAndPlan_EnumeratedGoalAdequatePlanKept proves the guard does not
// override a model plan that already covered every part — it only fires when
// the model produced FEWER steps than the enumeration.
func TestCreateAndPlan_EnumeratedGoalAdequatePlanKept(t *testing.T) {
	store, _ := NewStore(nil)
	threeStep := `{"steps":[{"id":"s1","description":"model step one, nicely phrased"},{"id":"s2","description":"model step two"},{"id":"s3","description":"model step three"}]}`
	r := NewRunner(store, newFakeOllama(t, threeStep), nil, nil, Config{Persona: testPersona, StepBudget: 15})

	task, err := r.CreateAndPlan(context.Background(), "sess", "1) a 2) b 3) c")
	if err != nil {
		t.Fatalf("CreateAndPlan: %v", err)
	}
	if len(task.Plan) != 3 || task.Plan[0].Description != "model step one, nicely phrased" {
		t.Fatalf("guard overrode an already-adequate model plan: %+v", task.Plan)
	}
}

// TestSplitEnumeratedParts is the pure, deterministic unit test of the split.
func TestSplitEnumeratedParts(t *testing.T) {
	got := SplitEnumeratedParts("Uc sey yap: 1) diskte /var altinda en buyuk 3 dosyayi bul, 2) kac tane CPU cekirdegi oldugunu soyle, 3) sandbox'ta hangi kullanicilar var listele.")
	want := []string{
		"diskte /var altinda en buyuk 3 dosyayi bul",
		"kac tane CPU cekirdegi oldugunu soyle",
		"sandbox'ta hangi kullanicilar var listele",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d parts, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("part %d = %q, want %q", i, got[i], want[i])
		}
	}
	// Non-enumerated → nil.
	if p := SplitEnumeratedParts("just find the biggest file"); p != nil {
		t.Errorf("free-form goal split to %#v, want nil", p)
	}
}

// TestCreateAndPlan_SkillStepLimitDoesNotClampEnumeration is the live-found
// regression: an enumerated multi-part goal whose FIRST part triggers a
// single-technique skill (largest-files, plan_step_limit:1) had its correctly
// decomposed 3-step plan truncated back to 1 by that skill cap — re-hiding
// parts 2 and 3. The enumeration count must be a hard floor the skill cap
// cannot cut beneath.
func TestCreateAndPlan_SkillStepLimitDoesNotClampEnumeration(t *testing.T) {
	store, _ := NewStore(nil)
	oneStep := `{"steps":[{"id":"s1","description":"find largest files in /var"}]}`
	reg, err := skill.LoadDir("../../../persona/v0.1/skills")
	if err != nil {
		t.Fatalf("load skills: %v", err)
	}
	r := NewRunner(store, newFakeOllama(t, oneStep), nil, nil,
		Config{Persona: testPersona, StepBudget: 15, Skills: reg})

	// Part 1 mentions "en buyuk dosya" to trigger the largest-files skill.
	goal := "Uc sey yap: 1) /var altinda en buyuk dosyalari bul, 2) CPU cekirdek sayisi, 3) /etc/passwd kullanicilari"
	task, err := r.CreateAndPlan(context.Background(), "sess", goal)
	if err != nil {
		t.Fatalf("CreateAndPlan: %v", err)
	}
	if len(task.Plan) != 3 {
		t.Fatalf("plan has %d steps, want 3 — a single-technique skill cap clamped an explicit 3-part enumeration: %+v", len(task.Plan), task.Plan)
	}
}

// TestCreateAndPlan_NaturalLanguageMultiPartGoalAudited proves the natural-
// language fallback: a goal with NO numbered markers ("check the OS and
// list the open ports" — real operator phrasing, not "1) ... 2) ...") whose
// planner call still under-decomposes to one step must be caught by the
// AuditGoalParts fallback, not silently accepted the way
// TestCreateAndPlan_FreeFormGoalNotTouched's genuinely single-intent goal
// correctly is. The fake LLM serves the plan call's single-step response
// first, then the audit call's two-line enumeration second.
func TestCreateAndPlan_NaturalLanguageMultiPartGoalAudited(t *testing.T) {
	store, err := NewStore(nil)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	oneStep := `{"steps":[{"id":"s1","description":"check what OS is running"}]}`
	auditResponse := "check what OS is running\nlist the open ports"
	r := NewRunner(store, newFakeOllama(t, oneStep, auditResponse), nil, nil, Config{Persona: testPersona, StepBudget: 15})

	goal := "check what OS is running and also list the open ports"
	task, err := r.CreateAndPlan(context.Background(), "sess", goal)
	if err != nil {
		t.Fatalf("CreateAndPlan: %v", err)
	}
	if len(task.Plan) != 2 {
		t.Fatalf("plan has %d steps, want 2 (audit-driven decomposition of an unmarked multi-part goal): %+v",
			len(task.Plan), task.Plan)
	}
}
