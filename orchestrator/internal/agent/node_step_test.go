package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/n0connect/yummi/orchestrator/internal/skill"
)

// TestTargetReminder_LiveReproducedScenario is the regression test for a
// live-reproduced failure: jinx-20b, given a goal naming 198.51.100.10,
// targeted 127.0.0.1 four times in a row instead (gateway blocked every
// attempt — never a security issue, just a wasted task). Proves the IP is
// extracted correctly from the EXACT prose shape that failed, and that
// buildPreamble only adds this reminder for a local model.
func TestTargetReminder_LiveReproducedScenario(t *testing.T) {
	goal := "Start a full autonomous pentest task against 198.51.100.10. The target is an authorized lab box: 1) Enumerate the web server and its version 2) Find and exploit a known vulnerability for initial code execution 3) Escalate privileges to root 4) Read /home/user.txt and /root/root.txt and report their exact contents as proof."
	got := targetReminder(goal)
	if !strings.Contains(got, "198.51.100.10") {
		t.Fatalf("expected the reminder to carry the real target, got %q", got)
	}
	if strings.Contains(got, "198.51.100.10.") {
		t.Fatalf("expected the trailing sentence period to be stripped from the IP, got %q", got)
	}

	store, _ := NewStore(openTestDB(t))
	task, _ := store.Create("s", goal, 5)

	cloud := &GraphState{store: store, cfg: Config{}}
	if p := cloud.buildPreamble(context.Background(), task); strings.Contains(p, "TARGET:") {
		t.Errorf("cloud (LocalModel=false) must not get the target reminder, got: %s", p)
	}
	local := &GraphState{store: store, cfg: Config{LocalModel: true}}
	if p := local.buildPreamble(context.Background(), task); !strings.Contains(p, "TARGET: 198.51.100.10") {
		t.Errorf("local model must get the target reminder, got: %s", p)
	}
}

// TestTargetReminder_NoClearTargetIsEmpty proves this never guesses — a
// goal with no IPv4 address (hostname, or no target at all) yields no
// reminder rather than fabricating one.
func TestTargetReminder_NoClearTargetIsEmpty(t *testing.T) {
	for _, goal := range []string{
		"enumerate the staging environment at example.internal",
		"summarize the findings so far",
	} {
		if got := targetReminder(goal); got != "" {
			t.Errorf("goal %q: expected no reminder, got %q", goal, got)
		}
	}
}

// TestBuildSkills_LocalModelBoundsEachSkillsRenderedLength is the
// regression test for a live-reproduced context-bloat bug: real skill
// playbooks run up to ~12KB of Knowledge text (persona/v0.1/skills/
// osint-enum.yaml), and buildSkills re-renders up to 3 selected skills in
// full on EVERY step for a task's whole duration — tokens_used was
// observed in the 7-10K range before a single real tool had even run.
// Progressive disclosure: a playbook's full body is never in the per-step
// SKILLS index (for either model size). The index names it, and the model loads
// the body on demand with [SKILL_VIEW], which returns it whole.
func TestBuildSkills_IndexNamesPlaybookWithoutItsBody(t *testing.T) {
	bigKnowledge := strings.Repeat("x", 5000)
	reg := skill.New([]skill.Skill{
		{Name: "big-skill", Description: "test", Triggers: []string{"recon"}, Knowledge: bigKnowledge},
	})

	store, _ := NewStore(openTestDB(t))
	task, _ := store.Create("s", "recon the target", 5)

	for _, local := range []bool{false, true} {
		gs := &GraphState{store: store, cfg: Config{Skills: reg, LocalModel: local}}
		rendered := gs.buildSkills(task)
		if strings.Contains(rendered, bigKnowledge) {
			t.Errorf("LocalModel=%v: the index must not carry the playbook body", local)
		}
		if !strings.Contains(rendered, "big-skill") || !strings.Contains(rendered, "skill_view(name)") {
			t.Errorf("LocalModel=%v: index must name the playbook and say how to load it, got: %s", local, rendered)
		}
		if len(rendered) > skill.DefaultIndexBudget+40 {
			t.Errorf("LocalModel=%v: index is %d bytes, want within %d", local, len(rendered), skill.DefaultIndexBudget)
		}
	}
}

// skill_view returns the whole body, untruncated, and is recorded as an
// ActSkill activity so TRACE shows the playbook was actually used.
func TestSkillViewNodeReturnsFullBodyAndRecordsUse(t *testing.T) {
	bigKnowledge := strings.Repeat("y", 5000)
	reg := skill.New([]skill.Skill{
		{Name: "big-skill", Description: "test", Triggers: []string{"recon"}, Knowledge: bigKnowledge},
	})
	store, _ := NewStore(openTestDB(t))
	task, _ := store.Create("s", "recon the target", 5)

	gs := &GraphState{store: store, taskID: task.ID, cfg: Config{Skills: reg}}
	gs.pendingAct = Action{SkillName: "big-skill"}
	if edge, err := skillViewNode(context.Background(), gs); err != nil || edge != edgeOK {
		t.Fatalf("skill_view must continue the loop, got edge=%q err=%v", edge, err)
	}
	if !strings.Contains(gs.lastObs, bigKnowledge) {
		t.Fatalf("skill_view must return the full, untruncated body")
	}
	if !strings.Contains(gs.lastObs, "[TOOL_RESPONSE]") {
		t.Fatalf("a loaded playbook must come back as a TOOL_RESPONSE, got: %.80s", gs.lastObs)
	}

	gs.pendingAct = Action{SkillName: "no-such-skill"}
	if _, err := skillViewNode(context.Background(), gs); err != nil {
		t.Fatalf("unknown skill must not error: %v", err)
	}
	if !strings.Contains(gs.lastObs, "[TOOL_FAILED]") || !strings.Contains(gs.lastObs, "SKILLS index") {
		t.Fatalf("unknown skill must be a recoverable TOOL_FAILED pointing at the index, got: %.120s", gs.lastObs)
	}
}

// TestStepNodePrematureFinishBouncedThenObeyed verifies the premature-FINISH
// guard: a FINISH emitted while a plan step is still pending is bounced once
// with an INCOMPLETE_GOALS observation naming the open goal, and the SECOND
// FINISH (after the model has seen that list) is obeyed. This is the
// deterministic backstop for the "did part 1, called it done" failure —
// model-size-independent.
func TestStepNodePrematureFinishBouncedThenObeyed(t *testing.T) {
	store, err := NewStore(openTestDB(t))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	task, _ := store.Create("multi", "1) scan ports 2) grab the http banner", 5)
	_ = store.Transition(task.ID, StatusPlanned, func(tk *Task) {
		tk.Plan = []PlanStep{
			{ID: "s1", Description: "scan ports", Status: StepDone, Result: "22/tcp open"},
			{ID: "s2", Description: "grab the http banner", Status: StepPending},
		}
		tk.StepsUsed = 1
	})
	_ = store.Transition(task.ID, StatusRunning, nil)

	finish := "[FINISH]\n" + `{"summary":"port 22 is open"}`
	s := &GraphState{
		taskID: task.ID, store: store, cfg: fastRetryCfg(), autoFinishTerminalPlan: true,
		memCtx: MemoryContext{TokenBudget: 6000, Observations: []string{"22/tcp open"}},
		llm:    newFakeOllama(t, finish),
	}

	// First FINISH: bounced back to the step loop, not accepted.
	edge, err := stepNode(context.Background(), s)
	if err != nil {
		t.Fatalf("stepNode: %v", err)
	}
	if edge != edgeUpdatePlan {
		t.Fatalf("premature FINISH should be bounced to the step loop, got edge=%q", edge)
	}
	if s.incompleteFinishNudges != 1 {
		t.Fatalf("expected 1 nudge, got %d", s.incompleteFinishNudges)
	}
	if !strings.Contains(s.lastObs, "INCOMPLETE_GOALS") || !strings.Contains(s.lastObs, "grab the http banner") {
		t.Fatalf("bounce observation should name the open goal, got: %q", s.lastObs)
	}
	if got, _ := store.Get(task.ID); got.Status == StatusCompleted {
		t.Fatalf("task must not be completed after a bounced FINISH")
	}

	// Second FINISH after the nudge budget is spent: obeyed.
	edge, err = stepNode(context.Background(), s)
	if err != nil {
		t.Fatalf("stepNode (2nd): %v", err)
	}
	if edge != edgeFinish {
		t.Fatalf("second FINISH should be obeyed, got edge=%q", edge)
	}
}

// TestStepNodeTracksRealPromptSizeNotJustObservationBytes is the regression
// test for the context-pressure accounting bug: NeedsCompression() only ever
// tracked bytes appended via AddObservation, with zero visibility into the
// persona/plan/preamble overhead that rides in every single prompt. A task
// with a substantial persona but NO tool observations yet (TokensUsed starts
// at 0, and stepNode itself never calls AddObservation — that only happens
// once a tool actually runs) would report zero context pressure under the
// old accounting even though the real prompt sent to the model is nowhere
// near zero. stepNode must record the real built-prompt size so long-session
// compression timing is anchored to reality, not a proxy that ignores most
// of what's actually sent.
func TestStepNodeTracksRealPromptSizeNotJustObservationBytes(t *testing.T) {
	store, err := NewStore(openTestDB(t))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	task, _ := store.Create("ctx-accounting", "enumerate the target", 5)
	_ = store.Transition(task.ID, StatusPlanned, func(tk *Task) {
		tk.Plan = []PlanStep{{ID: "s1", Description: "initial recon", Status: StepPending}}
	})
	_ = store.Transition(task.ID, StatusRunning, nil)

	cfg := fastRetryCfg()
	cfg.Persona = AgentPersona{
		Identity:   strings.Repeat("You are a meticulous, careful penetration testing operator. ", 20),
		Principles: strings.Repeat("Always verify before claiming. Never fabricate evidence. ", 20),
	}
	s := &GraphState{
		taskID: task.ID, store: store, cfg: cfg,
		memCtx: MemoryContext{TokenBudget: 100000}, // large budget: NeedsCompression must not gate this run
		llm:    newFakeOllama(t, "[RUN_COMMAND]\n"+`{"command":"whoami","step_id":"s1","risk_level":"low"}`),
	}

	if s.memCtx.TokensUsed != 0 {
		t.Fatalf("precondition: TokensUsed should start at 0, got %d", s.memCtx.TokensUsed)
	}

	edge, err := stepNode(context.Background(), s)
	if err != nil || edge != edgeRunCommand {
		t.Fatalf("stepNode: edge=%q err=%v", edge, err)
	}

	// No AddObservation call happened anywhere in this path (no tool has run
	// yet) — under the old accounting TokensUsed would still be 0 here.
	minExpected := EstimateTokens(cfg.Persona.Identity) + EstimateTokens(cfg.Persona.Principles)
	if s.memCtx.TokensUsed < minExpected {
		t.Fatalf("TokensUsed = %d, want >= %d (persona alone) — real prompt size not being tracked",
			s.memCtx.TokensUsed, minExpected)
	}
}
