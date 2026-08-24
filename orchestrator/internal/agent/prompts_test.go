package agent

import (
	"context"
	"fmt"
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

// TestCountEnumeratedParts locks the deterministic under-decomposition guard:
// the exact live-confirmed failure was the planner collapsing a clearly
// three-part goal into one plan step. This is the pure, deterministic signal
// that catches it — verified here without any model call.
func TestCountEnumeratedParts(t *testing.T) {
	cases := []struct {
		goal string
		want int
	}{
		// The exact live-failing goal.
		{"Uc sey yap: 1) diskte /var altinda en buyuk 3 dosyayi bul, 2) kac tane CPU cekirdegi oldugunu soyle, 3) sandbox'ta hangi kullanicilar var listele.", 3},
		{"1. do X 2. do Y", 2},
		{"do these: 1) alpha 2) beta 3) gamma 4) delta", 4},
		// Single free-form instruction — must NOT fire.
		{"find the largest file under /var", 0},
		{"list running processes", 0},
		// Numbers that are not enumeration markers — must NOT fire.
		{"check python 3.14 and top 3 files", 0},
		{"scan ports 1 through 3", 0},
		// A lone "1." is not a multi-part enumeration.
		{"step 1. do the thing", 0},
		// Non-consecutive / not starting at 1 — must NOT fire.
		{"see item 2) and item 5)", 0},
	}
	for _, c := range cases {
		if got := CountEnumeratedParts(c.goal); got != c.want {
			t.Errorf("CountEnumeratedParts(%q) = %d, want %d", c.goal, got, c.want)
		}
	}
}

func TestAuditGoalParts_ParsesMultiLineResponse(t *testing.T) {
	llm := newFakeOllama(t, "check what OS is running\nlist the open ports")
	parts := AuditGoalParts(context.Background(), llm, "check the OS and list open ports")
	if len(parts) != 2 {
		t.Fatalf("parts = %v, want 2 entries", parts)
	}
	if parts[0] != "check what OS is running" || parts[1] != "list the open ports" {
		t.Fatalf("parts = %v, unexpected content", parts)
	}
}

func TestAuditGoalParts_SingleLineReturnsNil(t *testing.T) {
	llm := newFakeOllama(t, "scan 10.0.0.1 for open ports")
	parts := AuditGoalParts(context.Background(), llm, "scan 10.0.0.1 for open ports")
	if parts != nil {
		t.Fatalf("parts = %v, want nil for a single-intent goal", parts)
	}
}

func TestAuditGoalParts_StripsBulletsAndNumberMarkers(t *testing.T) {
	llm := newFakeOllama(t, "1. check the OS\n2) list open ports")
	parts := AuditGoalParts(context.Background(), llm, "check the OS and list open ports")
	if len(parts) != 2 {
		t.Fatalf("parts = %v, want 2 entries", parts)
	}
	if parts[0] != "check the OS" || parts[1] != "list open ports" {
		t.Fatalf("parts = %v, markers/bullets not stripped cleanly", parts)
	}
}

func TestAuditGoalParts_TooManyLinesReturnsNil(t *testing.T) {
	var lines []string
	for i := range 12 {
		lines = append(lines, fmt.Sprintf("part %d", i))
	}
	llm := newFakeOllama(t, strings.Join(lines, "\n"))
	parts := AuditGoalParts(context.Background(), llm, "do a lot of things")
	if parts != nil {
		t.Fatalf("parts = %v, want nil for pathological line count", parts)
	}
}

func TestAuditGoalParts_NilLLMReturnsNil(t *testing.T) {
	if parts := AuditGoalParts(context.Background(), nil, "scan the host"); parts != nil {
		t.Fatalf("parts = %v, want nil with no LLM configured", parts)
	}
}

func TestLikelyMultiPart(t *testing.T) {
	cases := []struct {
		goal string
		want bool
	}{
		{"check the OS and list open ports", true},
		{"X'i tara ve Y'yi kontrol et", true},
		{"scan the host, then check the banner", true},
		{"scan the host; also check the banner", true},
		{"recon", false},
		{"go", false},
		{"delegate to two specialists", false},
		{"scan 10.0.0.1 for open ports", false},
	}
	for _, c := range cases {
		if got := LikelyMultiPart(c.goal); got != c.want {
			t.Errorf("LikelyMultiPart(%q) = %v, want %v", c.goal, got, c.want)
		}
	}
}

func TestSanitizeAgentText_StripsSpecialTokens(t *testing.T) {
	in := "<|channel|>final <|constrain|>finish<|channel|>commentary json<|message|>the OS is Linux"
	got := SanitizeAgentText(in)
	if strings.Contains(got, "<|") {
		t.Fatalf("special tokens not stripped: %q", got)
	}
	if !strings.Contains(got, "the OS is Linux") {
		t.Fatalf("real content lost: %q", got)
	}
}
