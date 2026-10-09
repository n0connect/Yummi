package eino

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/n0connect/yummi/orchestrator/internal/agent"
)

// The visible step counter must never report more steps than the budget. The
// ADK's own iteration limit is what stops an exhausted run; the counter is a
// display ceiling, so an extra final assistant event cannot show e.g. 3/2.
func TestEinoStepCounterNeverExceedsBudget(t *testing.T) {
	store, err := agent.NewStore(openTestDB(t))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	var mu sync.Mutex
	turn := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		mu.Lock()
		turn++
		n := turn
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "1", "object": "chat.completion", "model": "test",
			"choices": []map[string]any{{"index": 0, "finish_reason": "tool_calls", "message": map[string]any{
				"role": "assistant", "content": "", "tool_calls": []map[string]any{{
					"id": fmt.Sprintf("call_%d", n), "type": "function",
					"function": map[string]any{"name": "run_command", "arguments": fmt.Sprintf(`{"command":"echo step-%d","rationale":"r","risk_level":"low"}`, n)},
				}},
			}}},
		})
	}))
	defer srv.Close()

	execer := agent.NewStoreExecer(realShellStore(t), 5*time.Second)
	r := NewEinoRunner(store, nil, execer, Config{
		ModelBaseURL: srv.URL + "/v1", ModelName: "test", ApprovalMode: agent.ModeAuto, StepBudget: 2,
	})
	task, err := r.CreateAndPlan(context.Background(), "sess-counter", "keep running commands")
	if err != nil {
		t.Fatalf("CreateAndPlan: %v", err)
	}
	if err := r.Run(context.Background(), &task); err != nil {
		t.Fatalf("Run: %v", err)
	}
	final, _ := store.Get(task.ID)
	if final.StepsUsed > final.StepBudget {
		t.Fatalf("step counter %d exceeds budget %d", final.StepsUsed, final.StepBudget)
	}
	if final.Status != agent.StatusPaused {
		t.Fatalf("an exhausted budget must pause for the operator, got %s (lastErr=%q)", final.Status, final.LastError)
	}
}
