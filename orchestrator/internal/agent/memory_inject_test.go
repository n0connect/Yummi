package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
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

func TestToolsFromExecutedCommands(t *testing.T) {
	got := toolsFromCommands([]string{"nmap -sV 10.0.0.5", "nmap -p- 10.0.0.5", "gobuster dir -u http://x"})
	if len(got) != 2 || got[0] != "nmap" || got[1] != "gobuster" {
		t.Fatalf("want distinct [nmap gobuster] from executed commands, got %v", got)
	}
}

// buildMemory must key procedural retrieval off ACTUALLY-EXECUTED commands
// (s.recentCmds), so it lines up with how the executor records tactics. A
// tactic recorded for "nmap" must surface after an nmap command ran.
func TestBuildMemoryKeysOffExecutedCommands(t *testing.T) {
	ks, _ := knowledge.NewStore(nil)
	_ = ks.RecordTactic(context.Background(), "nmap -sV 10.0.0.5", "worked", nil, "")

	s := &GraphState{
		cfg:        Config{Retrieval: knowledge.NewStructuredRetrieval(ks)},
		recentCmds: []string{"nmap -sV 10.0.0.9"},
	}
	mem := s.buildMemory(context.Background(), Task{SessionID: "s1"})
	if !strings.Contains(mem, "MEMORY") || !strings.Contains(mem, "<TARGET>") {
		t.Fatalf("executed-command-keyed retrieval did not surface the nmap tactic:\n%s", mem)
	}

	// With no executed commands yet, no procedural tool key → no tactic surfaces.
	s.recentCmds = nil
	if mem := s.buildMemory(context.Background(), Task{SessionID: "s1"}); strings.Contains(mem, "<TARGET>") {
		t.Fatalf("tactic should not surface before any command ran:\n%s", mem)
	}
}
