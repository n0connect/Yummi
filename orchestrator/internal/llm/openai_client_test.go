package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIClientChat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("expected /chat/completions, got %s", r.URL.Path)
		}
		var req struct {
			Model    string `json:"model"`
			Stream   bool   `json:"stream"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Model != "test-model" {
			t.Errorf("expected model 'test-model', got %q", req.Model)
		}
		if req.Stream {
			t.Error("expected stream:false for Chat")
		}
		if len(req.Messages) != 2 || req.Messages[0].Role != "system" || req.Messages[1].Role != "user" {
			t.Fatalf("unexpected messages: %+v", req.Messages)
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"hello from openai-compat"}}]}`)
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "test-model", "test-key", 0, 0)
	resp, err := client.Chat(context.Background(), "system prompt", "user question")
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
	if resp != "hello from openai-compat" {
		t.Errorf("unexpected response: %q", resp)
	}
}

func TestOpenAIClientChatSendsAuthHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test-123" {
			t.Errorf("expected Authorization header, got %q", got)
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "m", "sk-test-123", 0, 0)
	if _, err := client.Chat(context.Background(), "s", "u"); err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
}

func TestOpenAIClientOmitsAuthHeaderForOllamaPlaceholder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("expected no Authorization header for placeholder key, got %q", got)
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "m", "ollama", 0, 0)
	if _, err := client.Chat(context.Background(), "s", "u"); err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
}

func TestOpenAIClientChatEmptyChoicesIsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"choices":[]}`)
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "m", "k", 0, 0)
	if _, err := client.Chat(context.Background(), "s", "u"); err == nil {
		t.Fatal("expected an error for empty choices")
	}
}

func TestOpenAIClientChatHTTPErrorIsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"invalid key"}}`)
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "m", "bad-key", 0, 0)
	if _, err := client.Chat(context.Background(), "s", "u"); err == nil {
		t.Fatal("expected an error for HTTP 401")
	}
}

// TestOpenAIClientChatStream exercises the real SSE wire format (data: lines,
// a [DONE] sentinel) — not Ollama's NDJSON, which is why this needs its own
// server-side fixture distinct from client_test.go's ChatStream coverage.
func TestOpenAIClientChatStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("test server does not support flushing")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunks := []string{"Hel", "lo", ", ", "world"}
		for _, c := range chunks {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", c)
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "m", "k", 0, 0)
	var streamed strings.Builder
	full, err := client.ChatStream(context.Background(), "system", "user", func(chunk string) {
		streamed.WriteString(chunk)
	})
	if err != nil {
		t.Fatalf("ChatStream failed: %v", err)
	}
	if full != "Hello, world" {
		t.Errorf("expected full accumulated text 'Hello, world', got %q", full)
	}
	if streamed.String() != full {
		t.Errorf("onChunk accumulation %q did not match returned full text %q", streamed.String(), full)
	}
}

func TestOpenAIClientChatHistoryStreamIncludesHistory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		// system + 2 history turns
		if len(req.Messages) != 3 {
			t.Fatalf("expected 3 messages (system + 2 history), got %d: %+v", len(req.Messages), req.Messages)
		}
		if req.Messages[1].Content != "first turn" || req.Messages[2].Content != "second turn" {
			t.Fatalf("history not forwarded correctly: %+v", req.Messages)
		}
		flusher := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ack\"}}]}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "m", "k", 0, 0)
	history := []Message{
		{Role: "user", Content: "first turn"},
		{Role: "assistant", Content: "second turn"},
	}
	full, err := client.ChatHistoryStream(context.Background(), "system", history, nil)
	if err != nil {
		t.Fatalf("ChatHistoryStream failed: %v", err)
	}
	if full != "ack" {
		t.Errorf("unexpected response: %q", full)
	}
}

func TestOpenAIClientModel(t *testing.T) {
	client := NewOpenAIClient("http://example.invalid", "z-ai/glm-5.2:free", "k", 0, 0)
	if got := client.Model(); got != "z-ai/glm-5.2:free" {
		t.Errorf("Model() = %q, want z-ai/glm-5.2:free", got)
	}
}

func TestOpenAIClientNilSafe(t *testing.T) {
	var client *OpenAIClient
	if client.Model() != "" {
		t.Error("expected empty Model() on nil client")
	}
	if _, err := client.Chat(context.Background(), "s", "u"); err == nil {
		t.Error("expected an error calling Chat on nil client")
	}
	if _, err := client.ChatStream(context.Background(), "s", "u", nil); err == nil {
		t.Error("expected an error calling ChatStream on nil client")
	}
	if _, err := client.ChatHistoryStream(context.Background(), "s", nil, nil); err == nil {
		t.Error("expected an error calling ChatHistoryStream on nil client")
	}
}

func TestOpenAIClientSetsOpenRouterHeaders(t *testing.T) {
	// openrouter.ai isn't reachable in a unit test, so this only verifies the
	// request-building logic (newRequest) sets the right headers for that
	// host — it doesn't perform a real network call.
	client := NewOpenAIClient("https://openrouter.ai/api/v1", "m", "k", 0, 0)
	req, err := client.newRequest(context.Background(), map[string]any{"model": "m"})
	if err != nil {
		t.Fatalf("newRequest failed: %v", err)
	}
	if req.Header.Get("HTTP-Referer") == "" {
		t.Error("expected HTTP-Referer header for openrouter.ai target")
	}
	if req.Header.Get("X-Title") == "" {
		t.Error("expected X-Title header for openrouter.ai target")
	}
}
