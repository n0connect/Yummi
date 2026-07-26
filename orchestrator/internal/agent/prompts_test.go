package agent

import (
	"strings"
	"testing"
)

func TestParsePlan(t *testing.T) {
	raw := "```json\n{\"steps\":[{\"id\":\"s1\",\"description\":\"recon\"},{\"id\":\"s2\",\"description\":\"enumerate\"}]}\n```"
	steps, err := ParsePlan(raw)
	if err != nil {
		t.Fatalf("parse plan: %v", err)
	}
	if len(steps) != 2 || steps[0].ID != "s1" || steps[1].Description != "enumerate" {
		t.Fatalf("unexpected steps: %+v", steps)
	}
	for _, s := range steps {
		if s.Status != StepPending {
			t.Errorf("step %s status = %s, want pending", s.ID, s.Status)
		}
	}
}

func TestParsePlanRejectsEmpty(t *testing.T) {
	if _, err := ParsePlan(`{"steps":[]}`); err == nil {
		t.Fatal("expected error for empty plan")
	}
	if _, err := ParsePlan("not json at all"); err == nil {
		t.Fatal("expected error for invalid json")
	}
}

func TestBuildStepPromptContainsState(t *testing.T) {
	task := Task{
		Goal:       "enumerate sandbox",
		StepsUsed:  3,
		StepBudget: 15,
		Plan: []PlanStep{
			{ID: "s1", Description: "whoami", Status: StepDone, Result: "pentester"},
			{ID: "s2", Description: "list ports", Status: StepPending},
		},
	}
	prompt := BuildStepPrompt(task, "[TOOL OUTPUT]\nuid=1000\n", "", "")
	for _, want := range []string{"enumerate sandbox", "s1", "s2", "3/15", "uid=1000"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("step prompt missing %q", want)
		}
	}
}

// TestBuildStepPromptWarnsAgainstUnexecutedFindings is a regression test for
// a live-observed hallucination: a completed Red Team task's FINISH findings
// included a specific "gobuster found /admin and /api" claim even though
// gobuster was never actually run that session (only curl/whatweb/nikto
// were, per the activity log) — the model reported results from a planned
// but never-executed step as if it had. The step prompt must explicitly warn
// against this.
func TestBuildStepPromptWarnsAgainstUnexecutedFindings(t *testing.T) {
	prompt := BuildStepPrompt(Task{Goal: "g"}, "", "", "")
	if !strings.Contains(prompt, "MUST be traceable to a command you actually ran") {
		t.Fatalf("expected an explicit anti-hallucination warning about findings, got:\n%s", prompt)
	}
}

func TestStepSystemPromptIncludesProtocol(t *testing.T) {
	p := StepSystemPrompt(testPersona)
	for _, tag := range []string{"[RUN_COMMAND]", "[TOOL_FAILED]", "[STOP_TASK]", "[UNKNOWN]"} {
		if !strings.Contains(p, tag) {
			t.Errorf("system prompt missing %s", tag)
		}
	}
}
