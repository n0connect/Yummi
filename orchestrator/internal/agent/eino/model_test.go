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

	cm, err := NewOllamaChatModel(context.Background(), srv.URL+"/v1", "qwen3:14b", "ollama", 0, 0, false)
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

	cm, err := NewOllamaChatModel(context.Background(), srv.URL+"/v1", "qwen3:14b", "ollama", 0, 16384, false)
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

// TestNewOllamaChatModel_ReasoningEffort is a live-long-form-testing-found
// regression test with a genuine reversal baked into it: forcing an explicit
// "medium" reasoning_effort for reasoning=false (this backend's default,
// since both gpt-oss tiers resolve Thinking:false) was tried first and
// regressed a real long-form task — at step 7 of a 78-step budget, with a
// full tool schema plus six prior tool-output turns already in context, the
// model's tool-call JSON got cut off mid-argument and the task hard-failed.
// eino-ext's MaxTokens is never set by this client, so nothing caps
// generation length explicitly; forcing reasoning_effort spends more of
// Ollama's own context-aware generation ceiling on the reasoning channel
// before the model reaches the actual tool-call arguments, and that ceiling
// shrinks as conversation history grows — exactly why it surfaced deep into
// a task, not on turn one. So reasoning=false must OMIT the field (server
// default, confirmed elsewhere to still reason adequately with zero observed
// truncation before this field existed at all) — not substitute a different
// explicit value. reasoning=true still maps to an explicit "high", an opt-in
// path a future caller can deliberately reach for.
func TestNewOllamaChatModel_ReasoningEffort(t *testing.T) {
	for _, tc := range []struct {
		name      string
		reasoning bool
		want      string // "" means the field must be absent entirely
	}{
		{"thinking off omits the field, does not substitute medium", false, ""},
		{"thinking on maps to high", true, "high"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gotBody []byte
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotBody, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"id": "test", "object": "chat.completion", "model": "gpt-oss:20b",
					"choices": []map[string]any{{
						"index":         0,
						"message":       map[string]any{"role": "assistant", "content": "pong"},
						"finish_reason": "stop",
					}},
				})
			}))
			defer srv.Close()

			cm, err := NewOllamaChatModel(context.Background(), srv.URL+"/v1", "gpt-oss:20b", "ollama", 0, 0, tc.reasoning)
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
			got, present := req["reasoning_effort"]
			if tc.want == "" {
				if present {
					t.Fatalf("reasoning_effort = %v, want field absent entirely:\n%s", got, gotBody)
				}
				return
			}
			if gotStr, _ := got.(string); gotStr != tc.want {
				t.Fatalf("reasoning_effort = %v, want %q:\n%s", got, tc.want, gotBody)
			}
		})
	}
}

// TestNewOllamaChatModel_OpenRouterNoNumCtx proves that when configured
// against OpenRouter or with a real API key, Ollama-specific options.num_ctx is omitted.
func TestNewOllamaChatModel_OpenRouterNoNumCtx(t *testing.T) {
	var gotAuth string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "test", "object": "chat.completion", "model": "anthropic/claude-3.5-sonnet",
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": "pong"},
				"finish_reason": "stop",
			}},
		})
	}))
	defer srv.Close()

	cm, err := NewOllamaChatModel(context.Background(), srv.URL+"/v1", "anthropic/claude-3.5-sonnet", "sk-or-test-key", 0, 16384, false)
	if err != nil {
		t.Fatalf("NewOllamaChatModel: %v", err)
	}
	if _, err := cm.Generate(context.Background(), []*schema.Message{schema.UserMessage("ping")}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if gotAuth != "Bearer sk-or-test-key" {
		t.Fatalf("Authorization = %q, want %q", gotAuth, "Bearer sk-or-test-key")
	}

	var req map[string]any
	if err := json.Unmarshal(gotBody, &req); err != nil {
		t.Fatalf("request body is not valid JSON: %v\n%s", err, gotBody)
	}
	if _, present := req["options"]; present {
		t.Fatalf("options field should NOT be present for non-Ollama requests:\n%s", gotBody)
	}
}
