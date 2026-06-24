package agent

import (
	"strings"
	"testing"
)

func TestBuildStepPromptIncludesMemory(t *testing.T) {
	task := Task{Goal: "enumerate host", StepBudget: 10}
	out := BuildStepPrompt(task, "", "", "MEMORY:\n  • [high] open redis — no auth\n")
	if !strings.Contains(out, "MEMORY:") || !strings.Contains(out, "open redis") {
		t.Fatalf("memory block not rendered:\n%s", out)
	}
}

func TestBuildStepPromptOmitsEmptyMemory(t *testing.T) {
	task := Task{Goal: "x", StepBudget: 10}
	out := BuildStepPrompt(task, "", "", "")
	if strings.Contains(out, "MEMORY:") {
		t.Fatal("empty memory should not render a MEMORY header")
	}
}
