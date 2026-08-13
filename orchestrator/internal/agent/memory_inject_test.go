package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
	"github.com/n0connect/yummi/orchestrator/internal/skill"
)

func TestPreambleInjectsRelevantSkill(t *testing.T) {
	reg := skill.New([]skill.Skill{
		{Name: "web", Triggers: []string{"web", "http"}, Knowledge: "fingerprint with whatweb first"},
		{Name: "recon", Triggers: []string{"nmap"}, Knowledge: "nmap -sV first"},
	})
	s := &GraphState{cfg: Config{Skills: reg}, recentCmds: []string{"whatweb http://x"}}

	pre := s.buildPreamble(context.Background(), Task{Goal: "assess the web target"})
	if !strings.Contains(pre, "SKILLS") || !strings.Contains(pre, "whatweb") {
		t.Fatalf("relevant web skill not injected into preamble:\n%s", pre)
	}
	// An unrelated skill must not appear.
	if strings.Contains(pre, "nmap -sV first") {
		t.Fatalf("irrelevant skill leaked into preamble:\n%s", pre)
	}
}

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
// tactic recorded for "nmap" must surface after an nmap command ran — as long
// as it is a DIFFERENT tactic than the one just run (the current-task command
// is suppressed to break the recall→re-run loop; see the suppression test).
func TestBuildMemoryKeysOffExecutedCommands(t *testing.T) {
	ks, _ := knowledge.NewStore(nil)
	// A prior-task tactic for the nmap tool, distinct from what runs below.
	_ = ks.RecordTactic(context.Background(), "nmap -p- 10.0.0.5", "worked", nil, "")

	s := &GraphState{
		cfg:        Config{Retrieval: knowledge.NewStructuredRetrieval(ks)},
		recentCmds: []string{"nmap -sV 10.0.0.9"}, // different pattern → not excluded
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

// TestBuildMemorySuppressesCurrentTaskCommands is the agent-level guard for the
// command-loop fix: a command already run this task (in recentCmds) must not be
// recalled by buildMemory, even though its tactic exists in the store. This
// breaks the "run → record → recall 'use it' → re-run" reinforcement loop.
func TestBuildMemorySuppressesCurrentTaskCommands(t *testing.T) {
	ks, _ := knowledge.NewStore(nil)
	ctx := context.Background()
	_ = ks.RecordTactic(ctx, "ps -ef", "worked", nil, "")

	s := &GraphState{
		cfg:        Config{Retrieval: knowledge.NewStructuredRetrieval(ks)},
		recentCmds: []string{"ps -ef"}, // the model just ran this
	}
	mem := s.buildMemory(ctx, Task{SessionID: "s1"})
	if strings.Contains(mem, "ps -ef") {
		t.Fatalf("current-task command must not be recalled into MEMORY:\n%s", mem)
	}
}

func TestBuildMemoryRecallsRememberedContextBeforeAnyCommand(t *testing.T) {
	ks, _ := knowledge.NewStore(nil)
	ctx := context.Background()
	if err := ks.RecordContext(ctx, knowledge.PersistentContext{
		SessionID: "s1", Key: "auth-map", Value: "login calls token.Verify then session.Rotate",
	}); err != nil {
		t.Fatalf("RecordContext: %v", err)
	}
	s := &GraphState{cfg: Config{Retrieval: knowledge.NewStructuredRetrieval(ks)}}
	mem := s.buildMemory(ctx, Task{SessionID: "s1", Goal: "trace the login and token flow"})
	if !strings.Contains(mem, "auth-map") || !strings.Contains(mem, "token.Verify") {
		t.Fatalf("REMEMBER context was not injected before the first command:\n%s", mem)
	}
}
