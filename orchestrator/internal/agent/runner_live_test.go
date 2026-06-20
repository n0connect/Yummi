package agent

import (
	"context"
	"os"
	osexec "os/exec"
	"testing"
	"time"

	"github.com/n0connect/yummi/orchestrator/internal/conductor"
	"github.com/n0connect/yummi/orchestrator/internal/execution"
	"github.com/n0connect/yummi/orchestrator/internal/llm"
)

// TestRunnerLive_RealModelRealContainer is a manual end-to-end smoke test against
// the REAL qwen3:8b model (Ollama) and the REAL n0 Kali container (docker exec).
// It is skipped unless YUMMI_LIVE=1 so it never runs in the normal suite.
//
// Run with:
//
//	YUMMI_LIVE=1 YUMMI_OLLAMA_URL=http://localhost:11434 \
//	YUMMI_OLLAMA_MODEL=qwen3:8b go test ./internal/agent/ -run TestRunnerLive -v -timeout 15m
//
// p* is intentionally bypassed (the host has no pstar socket); the validator
// still enforces scope and the goal targets only the local container.
func TestRunnerLive_RealModelRealContainer(t *testing.T) {
	if os.Getenv("YUMMI_LIVE") != "1" {
		t.Skip("live smoke: set YUMMI_LIVE=1 (needs Ollama + yummi-sandbox container)")
	}
	url := envOrDefault("YUMMI_OLLAMA_URL", "http://localhost:11434")
	model := envOrDefault("YUMMI_OLLAMA_MODEL", "qwen3:8b")
	container := envOrDefault("YUMMI_SANDBOX_CONTAINER", "yummi-sandbox")

	// Precondition: the n0 container must be reachable, or the run is meaningless
	// (every docker exec would fail with "No such container").
	if err := osexec.Command("docker", "exec", container, "true").Run(); err != nil {
		t.Skipf("live smoke: container %q not reachable (%v); start the stack first", container, err)
	}

	// Real n0 executor: run the command inside the Kali container via docker exec.
	es := execution.NewStore()
	es.SetProcessor(func(ctx context.Context, rec execution.Record) (string, error) {
		out, err := osexec.CommandContext(ctx, "docker", "exec", container, "sh", "-c", rec.Command).CombinedOutput()
		return string(out), err
	})

	persona, err := LoadAgentPersona(envOrDefault("YUMMI_PERSONA_DIR", "../../../persona/v0.1"))
	if err != nil {
		t.Skipf("live smoke: could not load agent persona: %v", err)
	}
	store, _ := NewStore(openTestDB(t))
	client := llm.NewClient(url, model)
	validator := conductor.NewValidator(conductor.ValidationConfig{})
	runner := NewRunner(store, client, validator, NewStoreExecer(es, 5*time.Minute),
		Config{StepBudget: 6, ApprovalTimeout: 90 * time.Second, SoftTimeout: 90 * time.Second,
			ApprovalMode: ModeAuto, Persona: persona})

	goal := "Identify which user we run as and the OS/kernel version on the local container (127.0.0.1), then summarize."
	task, err := runner.CreateAndPlan(context.Background(), "live-session", goal)
	if err != nil {
		t.Fatalf("CreateAndPlan: %v", err)
	}

	// Auto-approve any gate for this task so the run is unattended.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				if p, ok := runner.Gate().Pending(task.ID); ok {
					_ = runner.Approve(task.ID, p.ProposalID, true, "auto-approved (live smoke)")
				}
			}
		}
	}()
	t.Logf("PLAN (%d steps):", len(task.Plan))
	for _, s := range task.Plan {
		t.Logf("  [%s] %s", s.ID, s.Description)
	}

	if err := runner.Start(task.ID); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Poll to a terminal state, logging transitions.
	deadline := time.Now().Add(10 * time.Minute)
	last := TaskStatus("")
	var final Task
	for time.Now().Before(deadline) {
		tk, _ := store.Get(task.ID)
		if tk.Status != last {
			t.Logf("status: %s  (steps %d/%d)", tk.Status, tk.StepsUsed, tk.StepBudget)
			last = tk.Status
		}
		if tk.Status.Terminal() || tk.Status == StatusPaused {
			final = tk
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if final.ID == "" {
		t.Fatal("task did not reach a terminal state within the deadline")
	}

	t.Logf("FINAL status: %s", final.Status)
	t.Logf("summary: %s", final.Summary)
	for _, s := range final.Plan {
		t.Logf("  step [%s] %s — %s | result=%q error=%q", s.ID, s.Status, s.Description, s.Result, s.Error)
	}
	for _, f := range final.Findings {
		t.Logf("  finding: %s", f)
	}
	if final.LastError != "" {
		t.Logf("lastError: %s", final.LastError)
	}

	// The smoke test passes if the agent reached a clean terminal state.
	if final.Status != StatusCompleted {
		t.Fatalf("live run did not complete cleanly: %s (lastError=%q)", final.Status, final.LastError)
	}
}

func envOrDefault(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
