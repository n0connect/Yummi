package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/n0connect/yummi/orchestrator/internal/skillcurator"
)

// finishState builds a minimal GraphState for exercising finishNode directly
// (bypassing the full step loop, which isn't needed to test the terminal
// transition + the async skill-curator hook).
func finishState(t *testing.T, store *Store, task Task, cfg Config, outcome finishOutcome, msg string) *GraphState {
	t.Helper()
	return &GraphState{
		taskID:        task.ID,
		store:         store,
		cfg:           cfg,
		gate:          NewGate(),
		finishOutcome: outcome,
		finishMsg:     msg,
		memCtx:        MemoryContext{TokenBudget: 6000, Observations: []string{"nmap found port 22 open"}},
	}
}

// TestFinishNodeWarnsWhenFindingsReferenceAFailedTool is a regression test
// for a live-observed hallucination: a real DVWA pentest task ran `nikto -h
// http://172.20.0.6` TWICE, both aborted after running too long with zero
// real output — yet the model's own FINISH findings claimed "Nikto ile
// yapılan taramada bilinen güvenlik açıkları... tespit edildi" (nikto
// detected known vulnerabilities). The earlier prompt-level instruction
// ("every finding MUST be traceable to a command you actually ran") did not
// reliably prevent this. finishNode must now cross-check findings against
// which tools' plan steps actually failed and append a visible warning —
// never silently strip a finding, since only the operator can judge whether
// specific wording is accurate; the goal is making the risk visible, not
// pretending to perfectly classify it.
func TestFinishNodeWarnsWhenFindingsReferenceAFailedTool(t *testing.T) {
	store, _ := NewStore(openTestDB(t))
	task, err := store.Create("sess", "pentest 172.20.0.6", 15)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.Transition(task.ID, StatusPlanned, func(tk *Task) {
		tk.Plan = []PlanStep{
			{ID: "s1", Description: "HTTP headers", Status: StepPending},
			{ID: "s2", Description: "nikto scan", Status: StepPending},
		}
	}); err != nil {
		t.Fatalf("transition: %v", err)
	}
	if err := store.Transition(task.ID, StatusRunning, nil); err != nil {
		t.Fatalf("transition: %v", err)
	}
	markStepStatus(store, task.ID, "s1", "curl -sI http://172.20.0.6", StepDone, "HTTP/1.1 200 OK")
	markStepStatus(store, task.ID, "s2", "nikto -h http://172.20.0.6", StepFailed, "command aborted after running too long")

	cfg := fastRetryCfg()
	s := &GraphState{
		taskID:        task.ID,
		store:         store,
		cfg:           cfg,
		gate:          NewGate(),
		finishOutcome: outcomeCompleted,
		finishMsg:     "Pentest completed.",
		pendingAct: Action{
			Findings: []string{"Nikto ile yapılan taramada bilinen güvenlik açıkları tespit edildi."},
		},
	}
	if _, err := finishNode(context.Background(), s); err != nil {
		t.Fatalf("finishNode: %v", err)
	}

	got, _ := store.Get(task.ID)
	if !strings.Contains(got.Summary, "UNVERIFIED") || !strings.Contains(strings.ToLower(got.Summary), "nikto") {
		t.Fatalf("expected an UNVERIFIED warning naming nikto in the summary, got: %q", got.Summary)
	}
	// The original finding must still be present — never silently stripped.
	found := false
	for _, f := range got.Findings {
		if strings.Contains(f, "Nikto ile yapılan taramada") {
			found = true
		}
	}
	if !found {
		t.Fatalf("original finding must not be silently removed, got: %+v", got.Findings)
	}
}

// A finding that does NOT reference any failed tool must not trigger a
// warning — the guard should not cry wolf on legitimate, grounded findings.
func TestFinishNodeNoWarningWhenFindingsAreGrounded(t *testing.T) {
	store, _ := NewStore(openTestDB(t))
	task, err := store.Create("sess", "pentest 172.20.0.6", 15)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.Transition(task.ID, StatusPlanned, func(tk *Task) {
		tk.Plan = []PlanStep{{ID: "s1", Description: "nmap scan", Status: StepPending}}
	}); err != nil {
		t.Fatalf("transition: %v", err)
	}
	if err := store.Transition(task.ID, StatusRunning, nil); err != nil {
		t.Fatalf("transition: %v", err)
	}
	markStepStatus(store, task.ID, "s1", "nmap -sV 172.20.0.6", StepDone, "3306/tcp open mysql 5.5.47")

	s := &GraphState{
		taskID:        task.ID,
		store:         store,
		cfg:           fastRetryCfg(),
		gate:          NewGate(),
		finishOutcome: outcomeCompleted,
		finishMsg:     "Pentest completed.",
		pendingAct:    Action{Findings: []string{"nmap ile port 3306'da MySQL 5.5.47 açık bulundu."}},
	}
	if _, err := finishNode(context.Background(), s); err != nil {
		t.Fatalf("finishNode: %v", err)
	}

	got, _ := store.Get(task.ID)
	if strings.Contains(got.Summary, "UNVERIFIED") {
		t.Fatalf("expected no warning for a grounded finding, got: %q", got.Summary)
	}
}

func TestFinishNodeFiresSkillCuratorOnCompletedOnly(t *testing.T) {
	store, _ := NewStore(openTestDB(t))

	fired := make(chan skillcurator.Trajectory, 1)
	cfg := fastRetryCfg()
	cfg.SkillCurator = func(_ context.Context, traj skillcurator.Trajectory) { fired <- traj }

	task := newRunningTask(t, store, "enumerate the target")
	s := finishState(t, store, task, cfg, outcomeCompleted, "recon complete")

	if _, err := finishNode(context.Background(), s); err != nil {
		t.Fatalf("finishNode: %v", err)
	}

	select {
	case traj := <-fired:
		if traj.TaskID != task.ID || traj.SessionID != task.SessionID {
			t.Fatalf("unexpected trajectory: %+v", traj)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected SkillCurator to fire on task completion")
	}
}

func TestFinishNodeDoesNotFireSkillCuratorOnFailure(t *testing.T) {
	store, _ := NewStore(openTestDB(t))

	fired := make(chan skillcurator.Trajectory, 1)
	cfg := fastRetryCfg()
	cfg.SkillCurator = func(_ context.Context, traj skillcurator.Trajectory) { fired <- traj }

	task := newRunningTask(t, store, "enumerate the target")
	s := finishState(t, store, task, cfg, outcomeFailed, "boom")

	if _, err := finishNode(context.Background(), s); err != nil {
		t.Fatalf("finishNode: %v", err)
	}

	select {
	case traj := <-fired:
		t.Fatalf("SkillCurator must not fire on a failed task, got %+v", traj)
	case <-time.After(100 * time.Millisecond):
		// expected: nothing fired
	}
}

func TestFinishNodeDoesNotFireSkillCuratorOnStopped(t *testing.T) {
	store, _ := NewStore(openTestDB(t))

	fired := make(chan skillcurator.Trajectory, 1)
	cfg := fastRetryCfg()
	cfg.SkillCurator = func(_ context.Context, traj skillcurator.Trajectory) { fired <- traj }

	task := newRunningTask(t, store, "enumerate the target")
	s := finishState(t, store, task, cfg, outcomeStopped, "operator stopped")

	if _, err := finishNode(context.Background(), s); err != nil {
		t.Fatalf("finishNode: %v", err)
	}

	select {
	case traj := <-fired:
		t.Fatalf("SkillCurator must not fire on a stopped task, got %+v", traj)
	case <-time.After(100 * time.Millisecond):
	}
}

// TestFinishNodeSkillCuratorDoesNotBlockReturn proves the hook is fired async
// (a slow/hanging curator must never delay finishNode's return to the caller).
func TestFinishNodeSkillCuratorDoesNotBlockReturn(t *testing.T) {
	store, _ := NewStore(openTestDB(t))

	release := make(chan struct{})
	cfg := fastRetryCfg()
	cfg.SkillCurator = func(_ context.Context, _ skillcurator.Trajectory) { <-release }
	defer close(release)

	task := newRunningTask(t, store, "enumerate the target")
	s := finishState(t, store, task, cfg, outcomeCompleted, "done")

	done := make(chan struct{})
	go func() {
		_, _ = finishNode(context.Background(), s)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("finishNode blocked on a slow SkillCurator callback")
	}
}

func TestFinishNodeNilSkillCuratorIsNoop(t *testing.T) {
	store, _ := NewStore(openTestDB(t))
	task := newRunningTask(t, store, "enumerate the target")
	s := finishState(t, store, task, fastRetryCfg(), outcomeCompleted, "done") // SkillCurator left nil
	if _, err := finishNode(context.Background(), s); err != nil {
		t.Fatalf("finishNode with nil SkillCurator: %v", err)
	}
}
