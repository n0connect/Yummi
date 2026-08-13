package eino

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// TestNewOllamaChatModel_TalksToConfiguredBaseURL proves the constructed
// ChatModel sends its request to the base URL we configured — not to
// api.openai.com — since Ollama's OpenAI-compatible endpoint is the only
// backend this project ever talks to (no cloud provider).
func TestNewOllamaChatModel_TalksToConfiguredBaseURL(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "test", "object": "chat.completion", "model": "qwen3:14b",
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": "pong"},
				"finish_reason": "stop",
			}},
		})
	}))
	defer srv.Close()

	cm, err := NewOllamaChatModel(context.Background(), srv.URL+"/v1", "qwen3:14b")
	if err != nil {
		t.Fatalf("NewOllamaChatModel: %v", err)
	}

	msg, err := cm.Generate(context.Background(), []*schema.Message{
		schema.UserMessage("ping"),
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if msg.Content != "pong" {
		t.Fatalf("Content = %q, want %q", msg.Content, "pong")
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("request path = %q, want /v1/chat/completions", gotPath)
	}
}
