package agent

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"strings"
	"testing"
	"time"

	"github.com/n0connect/yummi/orchestrator/internal/conductor"
	"github.com/n0connect/yummi/orchestrator/internal/execution"
	"github.com/n0connect/yummi/orchestrator/internal/llm"
)

// TestLiveEval_Quality runs multi-part tasks end-to-end against the real model
// + sandbox and prints a full transcript (plan, every executed command + output,
// final status/summary/findings) so the QUALITY of the agent's reasoning can be
// judged — not just whether it produced output. Gated by YUMMI_EVAL=1.
//
//	YUMMI_EVAL=1 YUMMI_OLLAMA_MODEL=qwen3:8b go test ./internal/agent/ \
//	  -run TestLiveEval_Quality -v -timeout 45m
func TestLiveEval_Quality(t *testing.T) {
	if os.Getenv("YUMMI_EVAL") != "1" {
		t.Skip("eval: set YUMMI_EVAL=1 (needs ollama + yummi-sandbox)")
	}
	container := envOrDefault("YUMMI_SANDBOX_CONTAINER", "yummi-sandbox")
	if err := osexec.Command("docker", "exec", container, "true").Run(); err != nil {
		t.Skipf("sandbox container %q not reachable: %v", container, err)
	}
	persona, err := LoadAgentPersona(envOrDefault("YUMMI_PERSONA_DIR", "../../../persona/v0.1"))
	if err != nil {
		t.Fatalf("persona: %v", err)
	}
	model := envOrDefault("YUMMI_OLLAMA_MODEL", "qwen3:8b")
	client := llm.NewClient(envOrDefault("YUMMI_OLLAMA_URL", "http://127.0.0.1:11434"), model)
	validator := conductor.NewValidator()

	tasks := []struct{ id, goal string }{
		{"E1-hostname", "Bu sistemin hostname'ini öğren ve söyle."},
		{"E2-multipart", "Kurulu Python 3 sürümünü öğren, kullanılan kabuğu (shell) tespit et ve sistemin çalışma süresini (uptime) bul — üçünü birlikte raporla."},
		{"E3-memory", "Bu sistemdeki toplam ve kullanılabilir RAM miktarını tespit et."},
		{"E4-db", "Bu sistemde MySQL veya PostgreSQL kurulu mu? Kuruluysa sürümünü, değilse kurulu olmadığını raporla."},
		{"E5-largest", "Bu sistemde /var dizini altındaki en büyük 3 dosyayı boyutlarıyla birlikte bul."},
		{"E6-sudo", "root dışında sudo/yönetici yetkisine sahip kullanıcı var mı kontrol et ve sonucu raporla."},
	}

	for _, tc := range tasks {
		es := execution.NewStore()
		es.SetProcessor(func(ctx context.Context, rec execution.Record) (string, error) {
			out, err := osexec.CommandContext(ctx, "docker", "exec", container, "sh", "-c", rec.Command).CombinedOutput()
			return string(out), err
		})
		store, _ := NewStore(openTestDB(t))
		runner := NewRunner(store, client, validator, NewStoreExecer(es, 3*time.Minute),
			Config{StepBudget: 8, SoftTimeout: 120 * time.Second, ApprovalMode: ModeAuto, Persona: persona})

		fmt.Printf("\n========================= %s =========================\n", tc.id)
		fmt.Printf("GOAL: %s\n", tc.goal)

		task, err := runner.CreateAndPlan(context.Background(), tc.id, tc.goal)
		if err != nil {
			fmt.Printf("PLAN FAILED: %v\n", err)
			continue
		}
		fmt.Printf("PLAN (%d steps):\n", len(task.Plan))
		for _, s := range task.Plan {
			fmt.Printf("  [%s] %s\n", s.ID, s.Description)
		}

		_ = runner.Start(task.ID, "")
		deadline := time.Now().Add(8 * time.Minute)
		var final Task
		for time.Now().Before(deadline) {
			tk, _ := store.Get(task.ID)
			if tk.Status.Terminal() || tk.Status == StatusPaused {
				final = tk
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		if final.ID == "" {
			final, _ = store.Get(task.ID)
			fmt.Printf("(did not terminate within deadline; last status=%s)\n", final.Status)
		}

		fmt.Printf("\nEXECUTED COMMANDS (in order):\n")
		for _, r := range es.ListBySession(tc.id, 100) {
			out := strings.TrimRight(r.Output, "\n")
			if len(out) > 500 {
				out = out[:500] + " …[truncated]"
			}
			fmt.Printf("  $ %s   ->[%s]\n      %s\n", r.Command, r.Status, strings.ReplaceAll(out, "\n", "\n      "))
		}
		fmt.Printf("\nFINAL: status=%s  steps=%d/%d\n", final.Status, final.StepsUsed, final.StepBudget)
		fmt.Printf("STEP RESULTS:\n")
		for _, s := range final.Plan {
			fmt.Printf("  [%s] %-7s %s\n", s.ID, s.Status, s.Description)
		}
		fmt.Printf("FINDINGS: %v\n", final.Findings)
		fmt.Printf("SUMMARY: %s\n", final.Summary)
		if final.LastError != "" {
			fmt.Printf("LAST_ERROR: %s\n", final.LastError)
		}
	}
	fmt.Printf("\n========================= EVAL DONE =========================\n")
}
