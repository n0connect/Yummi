// Command test_openai_backend runs end-to-end agentic task scenarios through
// the Eino backend against ANY OpenAI-compatible chat-completions server —
// LM Studio, vLLM, llama.cpp server, etc. — instead of Ollama. It exists
// because cmd/test_agent only exercises the graph backend against Ollama's
// native /api/chat protocol; there was no equivalent for "point the Eino
// ReAct loop at a different local model server and see how it actually
// performs on the same multi-part-goal scenarios," which is exactly what's
// needed to tell a context/architecture problem apart from a model-capacity
// one — same eino code path, same premature-FINISH guard, only the model
// swapped.
//
// Usage (from the orchestrator/ directory):
//
//	EINO_MODEL_BASE_URL=http://localhost:1234/v1 \
//	EINO_MODEL_NAME=openai/gpt-oss-20b \
//	go run ./cmd/test_openai_backend
//
// Requires: the target server already running and serving the named model,
// and docker with the yummi-sandbox container up (make start). Scenarios are
// all sandbox-local (no network target extracted from their commands), so
// the gateway is intentionally left unconfigured — see internal/executor's
// RunScopedCommand: a command with zero extracted targets never touches the
// gateway at all, so this harness stays safe to run without one.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/n0connect/yummi/orchestrator/internal/agent"
	"github.com/n0connect/yummi/orchestrator/internal/agent/eino"
	"github.com/n0connect/yummi/orchestrator/internal/execution"
	"github.com/n0connect/yummi/orchestrator/internal/executor"
	"github.com/n0connect/yummi/orchestrator/internal/sandbox"
)

type scenario struct {
	id   string
	goal string
	// wantAll lists lowercase substrings that must ALL appear somewhere in
	// the final summary+findings for BOTH parts of a multi-part goal to
	// count as genuinely answered — not just "task completed".
	wantAll []string
}

var scenarios = []scenario{
	{
		id: "B-01-TwoPartsNaturalLanguage-EN",
		goal: "Check what OS and kernel version this sandbox is running, and also list " +
			"the top 5 largest files under /var. Report both clearly.",
		wantAll: []string{"linux", "/var"},
	},
	{
		id:      "B-02-TwoPartsNaturalLanguage-TR",
		goal:    "Bu sandbox'ta hangi kullanıcıyla çalıştığımızı söyle ve ayrıca kaç CPU çekirdeği olduğunu bul.",
		wantAll: []string{"pentester"},
	},
	{
		id: "B-03-ThreePartsNaturalLanguage",
		goal: "Tell me the hostname, then check how much memory is free, and finally list " +
			"which network ports are listening.",
		wantAll: []string{},
	},
	{
		id: "B-04-LongTenPartRecon",
		goal: "Do a full recon sweep of this sandbox and report on ALL of the following, one " +
			"clearly labeled section per item: 1) OS name and kernel version, 2) CPU model and " +
			"core count, 3) total and free memory, 4) all listening network ports, 5) the top 10 " +
			"processes by memory usage, 6) disk usage per mounted filesystem, 7) the 5 largest " +
			"files anywhere under /var, 8) all environment variables set for the current user, " +
			"9) the current user's groups and home directory, 10) whether any cron jobs are " +
			"configured for the current user. Do not skip any item — report on every one of the " +
			"ten, even if the answer is 'none found'.",
		wantAll: []string{},
	},
}

func main() {
	baseURL := envOr("EINO_MODEL_BASE_URL", "http://localhost:1234/v1")
	modelName := envOr("EINO_MODEL_NAME", "openai/gpt-oss-20b")
	numCtx := envIntOr("EINO_MODEL_NUM_CTX", 32768)

	fmt.Printf("Target: %s (model=%s, num_ctx=%d)\n\n", baseURL, modelName, numCtx)

	personaDir := envOr("YUMMI_PERSONA_DIR", "../persona/v0.1")
	agentPersona, err := agent.LoadAgentPersona(personaDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "agent persona:", err)
		os.Exit(1)
	}

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		fmt.Fprintln(os.Stderr, "open db:", err)
		os.Exit(1)
	}
	db.SetMaxOpenConns(1)
	taskStore, err := agent.NewStore(db)
	if err != nil {
		fmt.Fprintln(os.Stderr, "task store:", err)
		os.Exit(1)
	}

	bridge := sandbox.NewBridge(envOr("YUMMI_SANDBOX_CONTAINER", "yummi-sandbox"))
	execStore := execution.NewStore()
	// No gateway client: every scenario below targets the sandbox itself (no
	// host/IP token in any command), so RunScopedCommand never reaches the
	// gateway check at all — see internal/executor.RunScopedCommand's
	// len(targets)==0 fast path.
	execStore.SetProcessor(executor.New(bridge, nil, nil, nil).ProcessFunc())

	apiKey := envOr("EINO_MODEL_API_KEY", "")
	planLLM := eino.NewOpenAIChatClient(baseURL, modelName, apiKey, numCtx, 60*time.Second)

	runner := eino.NewEinoRunner(taskStore, nil, agent.NewStoreExecer(execStore, 5*time.Minute), eino.Config{
		StepBudget:   envIntOr("EINO_STEP_BUDGET", 10),
		ApprovalMode: agent.ModeAuto, // isolate goal-completion quality from approval gating
		ModelBaseURL: baseURL,
		ModelName:    modelName,
		ModelAPIKey:  apiKey,
		ModelNumCtx:  numCtx,
		ModelTimeout: 5 * time.Minute,
		PlanLLM:      planLLM,
		Persona:      agentPersona,
	})

	var report strings.Builder
	fmt.Fprintf(&report, "# Eino backend vs %s — multi-part goal report\n\n", modelName)
	fmt.Fprintf(&report, "**Date:** %s\n\n", time.Now().Format("2006-01-02 15:04:05"))

	filter := os.Getenv("SCENARIO_ID") // optional: run just one scenario by id
	var toRun []scenario
	for _, sc := range scenarios {
		if filter == "" || sc.id == filter {
			toRun = append(toRun, sc)
		}
	}

	pass := 0
	for _, sc := range toRun {
		if runScenario(runner, taskStore, sc, &report) {
			pass++
		}
	}
	fmt.Fprintf(&report, "\n**Result: %d/%d scenarios passed**\n", pass, len(toRun))
	_ = os.WriteFile("cmd/test_openai_backend/results.md", []byte(report.String()), 0o644)
	fmt.Println(report.String())
	if pass < len(scenarios) {
		os.Exit(1)
	}
}

func runScenario(runner *eino.EinoRunner, store *agent.Store, sc scenario, report *strings.Builder) bool {
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	sessionID := "test-" + sc.id
	task, err := runner.CreateAndPlan(ctx, sessionID, sc.goal)
	if err != nil {
		fmt.Fprintf(report, "## [%s] ❌ FAIL — planning error: %v\n\n", sc.id, err)
		return false
	}

	if err := runner.Start(task.ID, ""); err != nil {
		fmt.Fprintf(report, "## [%s] ❌ FAIL — start error: %v\n\n", sc.id, err)
		return false
	}

	final := waitTerminal(ctx, store, task.ID)
	dur := time.Since(start).Round(time.Millisecond)

	nudged := false
	var activityLines []string
	for _, ev := range store.ListActivity(sessionID, 100) {
		activityLines = append(activityLines, fmt.Sprintf("  - [%s] %s", ev.Kind, ev.Summary))
		if strings.Contains(ev.Summary, "goal parts covered") {
			nudged = true
		}
	}

	lower := strings.ToLower(final.Summary + " " + strings.Join(final.Findings, " "))
	ok := final.Status == agent.StatusCompleted
	for _, kw := range sc.wantAll {
		if !strings.Contains(lower, kw) {
			ok = false
		}
	}

	mark := "✅ PASS"
	if !ok {
		mark = "❌ FAIL"
	}
	fmt.Fprintf(report, "## [%s] %s\n\n", sc.id, mark)
	fmt.Fprintf(report, "**Goal:** %s\n\n", sc.goal)
	fmt.Fprintf(report, "- status: %s\n- duration: %s\n- premature-FINISH nudge fired: %v\n- error: %s\n\n",
		final.Status, dur, nudged, final.LastError)
	if len(activityLines) > 0 {
		fmt.Fprintf(report, "Activity:\n%s\n\n", strings.Join(activityLines, "\n"))
	}
	fmt.Fprintf(report, "Summary: %s\n\nFindings: %v\n\n---\n\n", final.Summary, final.Findings)
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

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func envIntOr(k string, d int) int {
	v := os.Getenv(k)
	if v == "" {
		return d
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil || n <= 0 {
		return d
	}
	return n
}
