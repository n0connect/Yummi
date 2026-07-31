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

// TestFinishNodeSanitizesSummaryAndFindings proves the fix for a confirmed
// gap: Task.Summary/Findings/Question are wire-visible to EVERY client (web,
// Tauri, AND internal/tui, which — unlike the React client's
// sanitizeOperatorText — applies no client-side cleaning at all). A model's
// own FINISH action is free text the model authored, which can carry
// reasoning-model <think> noise or an echoed protocol-tag line; this must be
// stripped server-side once, not left to a client that (as of internal/tui)
// may not do it at all.
func TestFinishNodeSanitizesSummaryAndFindings(t *testing.T) {
	store, _ := NewStore(openTestDB(t))
	task, err := store.Create("sess", "pentest 172.20.0.6", 15)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.Transition(task.ID, StatusPlanned, nil); err != nil {
		t.Fatalf("transition: %v", err)
	}
	if err := store.Transition(task.ID, StatusRunning, nil); err != nil {
		t.Fatalf("transition: %v", err)
	}

	s := &GraphState{
		taskID:        task.ID,
		store:         store,
		cfg:           fastRetryCfg(),
		gate:          NewGate(),
		finishOutcome: outcomeCompleted,
		finishMsg:     "<think>internal reasoning the operator should never see</think>Scan completed cleanly.\n[FINISH]",
		pendingAct: Action{
			Findings: []string{"<think>more reasoning</think>Port 22 open (SSH)."},
		},
	}
	if _, err := finishNode(context.Background(), s); err != nil {
		t.Fatalf("finishNode: %v", err)
	}

	got, _ := store.Get(task.ID)
	if strings.Contains(got.Summary, "<think>") || strings.Contains(got.Summary, "internal reasoning") {
		t.Fatalf("Summary must not leak <think> content, got: %q", got.Summary)
	}
	if strings.Contains(got.Summary, "[FINISH]") {
		t.Fatalf("Summary must not leak a standalone protocol-tag line, got: %q", got.Summary)
	}
	if !strings.Contains(got.Summary, "Scan completed cleanly") {
		t.Fatalf("Summary must still contain the model's real content, got: %q", got.Summary)
	}
	for _, f := range got.Findings {
		if strings.Contains(f, "<think>") {
			t.Fatalf("Findings must not leak <think> content, got: %+v", got.Findings)
		}
	}
}

// TestFinishNodeVerifiesFindingsAgainstRealStepOutput proves finishNode
// computes Task.FindingRecords server-side rather than trusting a
// self-reported verdict: a finding whose evidence is genuinely present in
// the completed step's recorded output earns Verified; one that names a step
// that never completed (or whose output doesn't contain the claimed
// evidence) is downgraded to Unsupported — regardless of how confident the
// model's own prose sounds.
func TestFinishNodeVerifiesFindingsAgainstRealStepOutput(t *testing.T) {
	store, _ := NewStore(openTestDB(t))
	task, err := store.Create("sess", "pentest 172.20.0.6", 15)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.Transition(task.ID, StatusPlanned, func(tk *Task) {
		tk.Plan = []PlanStep{
			{ID: "s1", Description: "port scan", Status: StepPending},
			{ID: "s2", Description: "nikto scan", Status: StepPending},
		}
	}); err != nil {
		t.Fatalf("transition: %v", err)
	}
	if err := store.Transition(task.ID, StatusRunning, nil); err != nil {
		t.Fatalf("transition: %v", err)
	}
	markStepStatus(store, task.ID, "s1", "nmap -sV 172.20.0.6", StepDone, "3306/tcp open mysql 5.5.47")
	markStepStatus(store, task.ID, "s2", "nikto -h 172.20.0.6", StepFailed, "command aborted after running too long")

	s := &GraphState{
		taskID:        task.ID,
		store:         store,
		cfg:           fastRetryCfg(),
		gate:          NewGate(),
		finishOutcome: outcomeCompleted,
		finishMsg:     "Pentest completed.",
		pendingAct: Action{
			FindingsDetail: []FindingDetail{
				{Title: "MySQL exposed", Evidence: "3306/tcp open mysql 5.5.47", StepID: "s1"},
				{Title: "Nikto findings", Evidence: "known vulnerabilities detected", StepID: "s2"},
				{Title: "Unbacked claim"}, // no evidence/step at all
			},
		},
	}
	if _, err := finishNode(context.Background(), s); err != nil {
		t.Fatalf("finishNode: %v", err)
	}

	got, _ := store.Get(task.ID)
	if len(got.FindingRecords) != 3 {
		t.Fatalf("expected 3 finding records, got %d: %+v", len(got.FindingRecords), got.FindingRecords)
	}
	byTitle := map[string]FindingRecord{}
	for _, r := range got.FindingRecords {
		byTitle[r.Title] = r
	}
	if r := byTitle["MySQL exposed"]; r.Verdict != VerdictVerified || r.OracleKind != OracleToolEvidence {
		t.Fatalf("expected MySQL finding verified via tool_evidence, got %+v", r)
	}
	if r := byTitle["Nikto findings"]; r.Verdict != VerdictUnsupported {
		t.Fatalf("expected Nikto finding unsupported (its step failed), got %+v", r)
	}
	if r := byTitle["Unbacked claim"]; r.Verdict != VerdictCandidate {
		t.Fatalf("expected unbacked claim to stay a candidate, got %+v", r)
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

// TestFinishNodeSkillCuratorSubmissionDoesNotBlockReturn proves finishNode
// only performs the worker's non-blocking submission callback.
func TestFinishNodeSkillCuratorSubmissionDoesNotBlockReturn(t *testing.T) {
	store, _ := NewStore(openTestDB(t))

	fired := make(chan struct{}, 1)
	cfg := fastRetryCfg()
	cfg.SkillCurator = func(_ context.Context, _ skillcurator.Trajectory) { fired <- struct{}{} }

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
		t.Fatal("finishNode blocked on curator submission")
	}
	select {
	case <-fired:
	case <-time.After(time.Second):
		t.Fatal("curator submission was not invoked")
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
