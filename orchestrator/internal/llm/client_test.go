package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

// ── Streaming ─────────────────────────────────────────────────────────────

// newNDJSONOllamaServer is a real httptest.Server (not a mock) that answers
// /api/chat with newline-delimited JSON, one line per element of chunks plus
// a final {done:true} line — the shape Ollama's stream:true response takes.
// Returns an accessor for the decoded request body, safe to call once the
// client call has returned (the handler always finishes writing before the
// client's scanner sees EOF, so there is a real happens-before edge, but the
// accessor is still mutex-guarded to keep `go test -race` happy).
func newNDJSONOllamaServer(t *testing.T, chunks ...string) (*httptest.Server, func() ollamaRequest) {
	t.Helper()
	var mu sync.Mutex
	var captured ollamaRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ollamaRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		captured = req
		mu.Unlock()

		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		for _, c := range chunks {
			line, _ := json.Marshal(ollamaResponse{Message: Message{Role: "assistant", Content: c}})
			_, _ = w.Write(append(line, '\n'))
			if flusher != nil {
				flusher.Flush()
			}
		}
		doneLine, _ := json.Marshal(ollamaResponse{Message: Message{Role: "assistant"}, Done: true})
		_, _ = w.Write(append(doneLine, '\n'))
	}))
	t.Cleanup(srv.Close)
	return srv, func() ollamaRequest {
		mu.Lock()
		defer mu.Unlock()
		return captured
	}
}

func TestClientChatStreamAccumulatesAndInvokesOnChunk(t *testing.T) {
	srv, getReq := newNDJSONOllamaServer(t, `{"in`, `tent":"ex`, `plain","message":"ok"}`)
	client := NewClient(srv.URL, "test-model")

	var mu sync.Mutex
	var received []string
	full, err := client.ChatStream(context.Background(), "system prompt", "user question", func(chunk string) {
		mu.Lock()
		received = append(received, chunk)
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("ChatStream failed: %v", err)
	}
	want := `{"intent":"explain","message":"ok"}`
	if full != want {
		t.Fatalf("expected accumulated %q, got %q", want, full)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 3 {
		t.Fatalf("expected 3 onChunk calls, got %d: %+v", len(received), received)
	}
	if strings.Join(received, "") != want {
		t.Fatalf("chunks did not reassemble to full text: %q", strings.Join(received, ""))
	}

	req := getReq()
	if !req.Stream {
		t.Error("expected stream=true in request body")
	}
	if req.Format != "json" {
		t.Errorf("expected format=json, got %q", req.Format)
	}
	if len(req.Messages) < 2 || req.Messages[0].Role != "system" {
		t.Error("expected first message to be system")
	}
}

func TestClientChatHistoryStreamSendsFullHistoryAndAllowsNilOnChunk(t *testing.T) {
	srv, getReq := newNDJSONOllamaServer(t, "history ", "response")
	client := NewClient(srv.URL, "test-model")

	history := []Message{
		{Role: "user", Content: "first message"},
		{Role: "assistant", Content: "first reply"},
	}
	// nil onChunk must not panic — ChatHistoryStream degrades to accumulate-only.
	full, err := client.ChatHistoryStream(context.Background(), "system", history, nil)
	if err != nil {
		t.Fatalf("ChatHistoryStream failed: %v", err)
	}
	if full != "history response" {
		t.Errorf("expected 'history response', got %q", full)
	}

	req := getReq()
	if len(req.Messages) != 3 {
		t.Errorf("expected 3 messages (system + 2 history), got %d", len(req.Messages))
	}
	if !req.Stream {
		t.Error("expected stream=true in request body")
	}
}

func TestClientChatStreamMidStreamOllamaErrorStopsAccumulation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"partial"}}` + "\n"))
		_, _ = w.Write([]byte(`{"error":"model overloaded"}` + "\n"))
	}))
	t.Cleanup(srv.Close)

	client := NewClient(srv.URL, "test-model")
	var chunks []string
	_, err := client.ChatStream(context.Background(), "sys", "user", func(c string) {
		chunks = append(chunks, c)
	})
	if err == nil {
		t.Fatal("expected error from a mid-stream ollama error line")
	}
	if !strings.Contains(err.Error(), "model overloaded") {
		t.Fatalf("expected error to mention model overloaded, got: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("expected exactly 1 onChunk call before the error line, got %d", len(chunks))
	}
}

func TestClientChatStreamSkipsMalformedLine(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"good"}}` + "\n"))
		_, _ = w.Write([]byte("not json at all\n"))
		doneLine, _ := json.Marshal(ollamaResponse{Done: true})
		_, _ = w.Write(append(doneLine, '\n'))
	}))
	t.Cleanup(srv.Close)

	client := NewClient(srv.URL, "test-model")
	full, err := client.ChatStream(context.Background(), "sys", "user", nil)
	if err != nil {
		t.Fatalf("expected a malformed line to be skipped, not fail the stream: %v", err)
	}
	if full != "good" {
		t.Errorf("expected 'good', got %q", full)
	}
}

func TestClientChatStreamEmptyResultIsError(t *testing.T) {
	srv, _ := newNDJSONOllamaServer(t, "   ", "\n")
	client := NewClient(srv.URL, "test-model")

	_, err := client.ChatStream(context.Background(), "sys", "user", nil)
	if err == nil {
		t.Fatal("expected an error for empty/whitespace-only accumulated content")
	}
	if !strings.Contains(err.Error(), "empty message") {
		t.Fatalf("expected empty-message error, got: %v", err)
	}
}

func TestClientChatStreamNilClientSafe(t *testing.T) {
	var c *Client
	if _, err := c.ChatStream(context.Background(), "sys", "msg", nil); err == nil {
		t.Error("nil client ChatStream() should return error")
	}
	if _, err := c.ChatHistoryStream(context.Background(), "sys", nil, nil); err == nil {
		t.Error("nil client ChatHistoryStream() should return error")
	}
}
