package eino

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/n0connect/yummi/orchestrator/internal/agent"
)

func TestEinoRunner_SatisfiesTaskRunner(t *testing.T) {
	var _ agent.TaskRunner = (*EinoRunner)(nil)
}

// TestEinoRunner_Run_CallsFinishAndCompletesTask scripts a single-turn
// tool-calling response (finish, with no other tool calls) against a real
// httptest server shaped like Ollama's OpenAI-compatible endpoint — no mock
// of Yummi's own code, matching project convention.
func TestEinoRunner_Run_CallsFinishAndCompletesTask(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "1", "object": "chat.completion", "model": "test",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role": "assistant", "content": "",
					"tool_calls": []map[string]any{{
						"id": "call_1", "type": "function",
						"function": map[string]any{
							"name":      "finish",
							"arguments": `{"summary":"done: found nothing of note","findings":[]}`,
						},
					}},
				},
				"finish_reason": "tool_calls",
			}},
		})
	}))
	defer srv.Close()

	store, err := agent.NewStore(nil) // in-memory, matches project's ":memory:" convention
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	r := NewEinoRunner(store, nil, nil, Config{
		ModelBaseURL: srv.URL + "/v1", ModelName: "test", ApprovalMode: agent.ModeAuto,
	})

	task, err := r.CreateAndPlan(context.Background(), "session-1", "test goal")
	if err != nil {
		t.Fatalf("CreateAndPlan: %v", err)
	}
	if task.Status != agent.StatusPlanned {
		t.Fatalf("status after CreateAndPlan = %s, want planned", task.Status)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Run(ctx, &task); err != nil {
		t.Fatalf("Run: %v", err)
	}

	final, _ := store.Get(task.ID)
	if final.Status != agent.StatusCompleted {
		t.Fatalf("status = %s, want completed", final.Status)
	}
}
