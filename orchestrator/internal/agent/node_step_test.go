package agent

import (
	"context"
	"strings"
	"testing"
)

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
		llm: newFakeOllama(t, "[RUN_COMMAND]\n"+`{"command":"whoami","step_id":"s1","risk_level":"low"}`),
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
