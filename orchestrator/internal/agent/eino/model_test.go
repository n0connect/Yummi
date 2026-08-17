package eino

import (
	"context"
	"encoding/json"
	"io"
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

	cm, err := NewOllamaChatModel(context.Background(), srv.URL+"/v1", "qwen3:14b", 0, 0)
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

// TestNewOllamaChatModel_InjectsNumCtxOption is a live-long-form-testing-found
// regression test: the OpenAI chat-completions schema this client speaks has
// no context-window field, so this backend's calls previously specified no
// options at all. internal/llm.Client's own num_ctx fix only covers Ollama's
// native /api/generate and /api/chat endpoints (Preload and conductor chat) —
// a live run found this backend's actual model calls silently running Ollama
// at its own default context (observed: 4096, not the configured tier's
// 16384) whenever Ollama needed to (re)apply options for this endpoint's
// request stream. Proves numCtx is injected as a top-level Ollama-specific
// "options.num_ctx" field via ExtraFields.
func TestNewOllamaChatModel_InjectsNumCtxOption(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
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

	cm, err := NewOllamaChatModel(context.Background(), srv.URL+"/v1", "qwen3:14b", 0, 16384)
	if err != nil {
		t.Fatalf("NewOllamaChatModel: %v", err)
	}
	if _, err := cm.Generate(context.Background(), []*schema.Message{schema.UserMessage("ping")}); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(gotBody, &req); err != nil {
		t.Fatalf("request body is not valid JSON: %v\n%s", err, gotBody)
	}
	options, ok := req["options"].(map[string]any)
	if !ok {
		t.Fatalf("request has no top-level \"options\" field:\n%s", gotBody)
	}
	if numCtx, _ := options["num_ctx"].(float64); numCtx != 16384 {
		t.Fatalf("options.num_ctx = %v, want 16384:\n%s", options["num_ctx"], gotBody)
	}
}
