package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientChat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("expected /api/chat, got %s", r.URL.Path)
		}

		var req ollamaRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if req.Model == "" {
			t.Error("expected model in request")
		}
		if req.Format != "json" {
			t.Errorf("expected format=json, got %q", req.Format)
		}
		// System message should be first
		if len(req.Messages) < 2 || req.Messages[0].Role != "system" {
			t.Error("expected first message to be system")
		}

		resp := ollamaResponse{
			Message: Message{Role: "assistant", Content: `{"intent":"explain","message":"ok","confidence":0.9}`},
			Done:    true,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-model")
	response, err := client.Chat(context.Background(), "system prompt", "user question")
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
	if response == "" {
		t.Error("expected non-empty response")
	}
}

func TestClientChatEmptyMessageIsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := ollamaResponse{
			Message: Message{Role: "assistant", Content: "   \n  "}, // whitespace only
			Done:    true,
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-model")
	_, err := client.Chat(context.Background(), "system", "user")
	if err == nil {
		t.Fatal("expected an error for an empty/whitespace model message")
	}
	if !strings.Contains(err.Error(), "empty message") {
		t.Fatalf("expected empty-message error, got: %v", err)
	}
}

func TestClientChatHistory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ollamaRequest
		json.NewDecoder(r.Body).Decode(&req)

		// system + 2 history messages
		if len(req.Messages) != 3 {
			t.Errorf("expected 3 messages (system + 2 history), got %d", len(req.Messages))
		}

		resp := ollamaResponse{
			Message: Message{Role: "assistant", Content: "history response"},
			Done:    true,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-model")
	history := []Message{
		{Role: "user", Content: "first message"},
		{Role: "assistant", Content: "first reply"},
	}
	response, err := client.ChatHistory(context.Background(), "system", history)
	if err != nil {
		t.Fatalf("ChatHistory failed: %v", err)
	}
	if response != "history response" {
		t.Errorf("expected 'history response', got %q", response)
	}
}

func TestClientPing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("expected /api/tags, got %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"models": []any{}})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-model")
	if err := client.Ping(context.Background()); err != nil {
		t.Fatalf("Ping failed: %v", err)
	}
}

func TestClientTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-model")
	client.httpClient.Timeout = 100 * time.Millisecond

	_, err := client.Chat(context.Background(), "sys", "test")
	if err == nil {
		t.Error("expected timeout error")
	}
}

func TestClientNilSafe(t *testing.T) {
	var c *Client
	if c.Model() != "" {
		t.Error("nil client Model() should return empty")
	}
	_, err := c.Chat(context.Background(), "sys", "msg")
	if err == nil {
		t.Error("nil client Chat() should return error")
	}
}

func TestClientPreload(t *testing.T) {
	// KeepAlive is any (int or string) — use json.Number for flexible decoding.
	var capturedBody struct {
		Model     string          `json:"model"`
		Prompt    string          `json:"prompt"`
		Stream    bool            `json:"stream"`
		KeepAlive json.RawMessage `json:"keep_alive"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			t.Errorf("expected /api/generate, got %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&capturedBody); err != nil {
			t.Errorf("decode preload body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		// Ollama generate response (model loaded, zero tokens)
		w.Write([]byte(`{"model":"qwen3:14b","response":"","done":true}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "qwen3:14b")
	if err := client.Preload(context.Background()); err != nil {
		t.Fatalf("Preload failed: %v", err)
	}

	// Verify the request sent the correct fields
	if capturedBody.Model != "qwen3:14b" {
		t.Errorf("expected model qwen3:14b, got %q", capturedBody.Model)
	}
	if capturedBody.Prompt != "" {
		t.Errorf("expected empty prompt for preload, got %q", capturedBody.Prompt)
	}
	if capturedBody.Stream {
		t.Error("expected stream=false for preload")
	}
	// keep_alive must be the integer -1 (JSON number), not the string "-1".
	// Ollama's duration parser rejects bare "-1" without a unit suffix.
	if string(capturedBody.KeepAlive) != "-1" {
		t.Errorf("expected keep_alive=-1 (integer), got %s", capturedBody.KeepAlive)
	}
}

func TestClientPreloadNilSafe(t *testing.T) {
	var c *Client
	if err := c.Preload(context.Background()); err == nil {
		t.Error("nil client Preload() should return error")
	}
}

func TestQwen3ThinkTopLevel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ollamaRequest
		json.NewDecoder(r.Body).Decode(&req)

		// think must be top-level false — NOT inside options — so Ollama's template
		// renderer suppresses Qwen3 <think> tokens at the API level.
		if req.Think == nil {
			t.Error("expected think field to be set at top level")
		} else if *req.Think {
			t.Error("expected think=false (thinking disabled for fast responses)")
		}
		// System message must NOT have /no_think prefix — API-level control is used.
		if len(req.Messages) > 0 && req.Messages[0].Role == "system" {
			if strings.HasPrefix(req.Messages[0].Content, "/no_think") {
				t.Errorf("system message must not have /no_think prefix: %q", req.Messages[0].Content)
			}
		}

		resp := ollamaResponse{Message: Message{Role: "assistant", Content: "ok"}, Done: true}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "qwen3:14b")
	// DefaultQwen3Options sets Think=&false — must arrive at top-level, not options.
	client.Chat(context.Background(), "You are Yummi", "scan target")
}
