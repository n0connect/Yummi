// Command test_agent runs end-to-end agentic task scenarios against a live
// Ollama + sandbox stack. Usage (from the orchestrator/ directory):
//
//	YUMMI_OLLAMA_URL=http://localhost:11434 go run ./cmd/test_agent
//
// Requires: ollama running with the configured model, docker with the
// yummi-sandbox container up (make start).
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/n0connect/yummi/orchestrator/internal/agent"
	"github.com/n0connect/yummi/orchestrator/internal/conductor"
	"github.com/n0connect/yummi/orchestrator/internal/execution"
	"github.com/n0connect/yummi/orchestrator/internal/executor"
	"github.com/n0connect/yummi/orchestrator/internal/gateway"
	"github.com/n0connect/yummi/orchestrator/internal/sandbox"
)

type scenario struct {
	id, goal     string
	wantKeywords []string // expected in summary/findings (lowercase)
	maxSteps     int
}

var scenarios = []scenario{
	{
		id:           "A-01-SysRecon",
		goal:         "Bu sandbox sisteminde hangi kullanıcıyla çalıştığımızı ve işletim sistemi sürümünü tespit et, sonra özetle.",
		wantKeywords: []string{"pentester"},
		maxSteps:     6,
	},
	{
		id:           "A-02-MultiStep",
		goal:         "Identify the running processes and listening sockets on the sandbox container, then summarize anything notable.",
		wantKeywords: []string{},
		maxSteps:     8,
	},
}

func main() {
	ollamaURL := os.Getenv("YUMMI_OLLAMA_URL")
	if ollamaURL == "" {
		fmt.Fprintln(os.Stderr, "YUMMI_OLLAMA_URL is required")
		os.Exit(1)
	}

	personaDir := envOr("YUMMI_PERSONA_DIR", "../persona/v0.1")
	engine, err := conductor.NewEngine(personaDir, ollamaURL, os.Getenv("YUMMI_OLLAMA_MODEL"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "engine:", err)
		os.Exit(1)
	}
	if engine.LLM() == nil {
		fmt.Fprintln(os.Stderr, "LLM not configured (check YUMMI_OLLAMA_URL)")
		os.Exit(1)
	}

	bridge := sandbox.NewBridge(envOr("YUMMI_SANDBOX_CONTAINER", "yummi-sandbox"))
	pc := gateway.NewClient(envOr("YUMMI_GATEWAY_SOCKET", "/run/yummi/gateway.sock"))

	execStore := execution.NewStore()
	execStore.SetProcessor(executor.New(bridge, pc, nil, nil).ProcessFunc())

	agentPersona, err := agent.LoadAgentPersona(personaDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "agent persona:", err)
		os.Exit(1)
	}
	taskStore, _ := agent.NewStore(nil)
	runner := agent.NewRunner(taskStore, engine.LLM(), engine.Validator(),
		agent.NewStoreExecer(execStore, 5*time.Minute),
		agent.Config{StepBudget: 10, ApprovalTimeout: 30 * time.Second, Persona: agentPersona})

	// Auto-approver: integration runs unattended, so approve every gate and
	// record that it fired (the gate path itself is what we are testing).
	go autoApprove(runner, taskStore)

	var report strings.Builder
	report.WriteString("# Yummi — Agentic Task Loop Integration Report\n\n")
	fmt.Fprintf(&report, "**Model:** %s  \n**Date:** %s\n\n",
		engine.OllamaModel(), time.Now().Format("2006-01-02 15:04:05"))

	pass := 0
	for _, sc := range scenarios {
		if runScenario(runner, taskStore, sc, &report) {
			pass++
		}
	}
	fmt.Fprintf(&report, "\n**Result: %d/%d scenarios passed**\n", pass, len(scenarios))
	_ = os.WriteFile("cmd/test_agent/results.md", []byte(report.String()), 0o644)
	fmt.Println(report.String())
	if pass < len(scenarios) {
		os.Exit(1)
	}
}

func runScenario(runner *agent.Runner, store *agent.Store, sc scenario, report *strings.Builder) bool {
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	task, err := runner.CreateAndPlan(ctx, "test-"+sc.id, sc.goal)
	if err != nil {
		fmt.Fprintf(report, "## [%s] ❌ FAIL — planning error: %v\n\n", sc.id, err)
		return false
	}
	fmt.Fprintf(report, "## [%s] plan: %d steps\n\n", sc.id, len(task.Plan))
	for _, s := range task.Plan {
		fmt.Fprintf(report, "- %s: %s\n", s.ID, s.Description)
	}

	if err := runner.Start(task.ID, ""); err != nil {
		fmt.Fprintf(report, "\n❌ FAIL — start error: %v\n\n", err)
		return false
	}

	final := waitTerminal(ctx, store, task.ID)
	dur := time.Since(start).Round(time.Millisecond)

	ok := final.Status == agent.StatusCompleted && final.StepsUsed <= sc.maxSteps
	lower := strings.ToLower(final.Summary + " " + strings.Join(final.Findings, " "))
	for _, kw := range sc.wantKeywords {
		if !strings.Contains(lower, kw) {
			ok = false
			fmt.Fprintf(report, "\n- missing keyword: %q\n", kw)
		}
	}

	mark := "✅ PASS"
	if !ok {
		mark = "❌ FAIL"
	}
	fmt.Fprintf(report, "\n**%s** · status=%s · steps=%d/%d · %s\n\nSummary: %s\n\n",
		mark, final.Status, final.StepsUsed, final.StepBudget, dur, final.Summary)
	return ok
}

func waitTerminal(ctx context.Context, store *agent.Store, id string) agent.Task {
	for {
		t, _ := store.Get(id)
		if t.Status.Terminal() || t.Status == agent.StatusPaused {
			return t
		}
		select {
		case <-ctx.Done():
			return t
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func autoApprove(runner *agent.Runner, store *agent.Store) {
	for {
		time.Sleep(300 * time.Millisecond)
		// Approve any pending gate on awaiting tasks across known test sessions.
		for _, sc := range scenarios {
			for _, t := range store.ListBySession("test-"+sc.id, 10) {
				if t.Status != agent.StatusAwaitingApproval {
					continue
				}
				if p, ok := runner.Gate().Pending(t.ID); ok {
					slog.Info("auto-approving", "task", t.ID, "command", p.Command)
					_ = runner.Approve(t.ID, p.ProposalID, true, "auto-approved (integration test)")
				}
			}
		}
	}
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
