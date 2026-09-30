package eino

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAIChatClient_Chat_ParsesResponse(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{"role": "assistant", "content": "hello back"},
			}},
		})
	}))
	defer srv.Close()

	c := NewOpenAIChatClient(srv.URL, "test-model", "ollama", 0, 0)
	got, err := c.Chat(context.Background(), "sys prompt", "user prompt")
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if got != "hello back" {
		t.Fatalf("got %q, want %q", got, "hello back")
	}
	if gotBody["model"] != "test-model" {
		t.Fatalf("request model = %v, want test-model", gotBody["model"])
	}
}

func TestOpenAIChatClient_Chat_TrimsTrailingSlashInBaseURL(t *testing.T) {
	var hit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = r.URL.Path == "/chat/completions"
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "ok"}}},
		})
	}))
	defer srv.Close()

	c := NewOpenAIChatClient(srv.URL+"/", "test-model", "ollama", 0, 0)
	if _, err := c.Chat(context.Background(), "s", "u"); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if !hit {
		t.Fatalf("expected exactly one clean /chat/completions request, trailing slash in baseURL wasn't handled")
	}
}

func TestOpenAIChatClient_Chat_NonOKStatusIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"model not loaded"}`))
	}))
	defer srv.Close()

	c := NewOpenAIChatClient(srv.URL, "test-model", "ollama", 0, 0)
	_, err := c.Chat(context.Background(), "s", "u")
	if err == nil {
		t.Fatal("expected an error for a non-200 response, got nil")
	}
}

func TestOpenAIChatClient_Chat_EmptyChoicesIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{}})
	}))
	defer srv.Close()

	c := NewOpenAIChatClient(srv.URL, "test-model", "ollama", 0, 0)
	_, err := c.Chat(context.Background(), "s", "u")
	if err == nil {
		t.Fatal("expected an error for an empty choices array, got nil")
	}
}

// TestOpenAIChatClient_Chat_SendsNumCtxWhenSet is the regression test for a
// live-observed bug: without this, an AuditGoalParts call could be the FIRST
// request to touch a not-yet-loaded model, and Ollama fixes a model's context
// window to whatever the loading request specified — so a client that never
// sends num_ctx can silently strand the model at Ollama's own small default,
// and the ReAct loop's own later (correct) num_ctx request gets served
// against that already-fixed, too-small context instead.
func TestOpenAIChatClient_Chat_SendsNumCtxWhenSet(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "ok"}}},
		})
	}))
	defer srv.Close()

	c := NewOpenAIChatClient(srv.URL, "test-model", "ollama", 32768, 0)
	if _, err := c.Chat(context.Background(), "s", "u"); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	options, ok := gotBody["options"].(map[string]any)
	if !ok {
		t.Fatalf("request has no options field: %+v", gotBody)
	}
	if numCtx, ok := options["num_ctx"].(float64); !ok || int(numCtx) != 32768 {
		t.Fatalf("options.num_ctx = %v, want 32768", options["num_ctx"])
	}
}

// TestOpenAIChatClient_Chat_OmitsOptionsWhenNumCtxUnset proves a server that
// doesn't understand the Ollama-specific num_ctx extension never receives an
// unwanted options field when the caller didn't ask for one.
func TestOpenAIChatClient_Chat_OmitsOptionsWhenNumCtxUnset(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "ok"}}},
		})
	}))
	defer srv.Close()

	c := NewOpenAIChatClient(srv.URL, "test-model", "ollama", 0, 0)
	if _, err := c.Chat(context.Background(), "s", "u"); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if _, present := gotBody["options"]; present {
		t.Fatalf("options field present when numCtx was unset: %+v", gotBody)
	}
}

func TestOpenAIChatClient_Chat_AuthorizationAndOpenRouter(t *testing.T) {
	var gotAuth string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "ok"}}},
		})
	}))
	defer srv.Close()

	c := NewOpenAIChatClient(srv.URL, "anthropic/claude-3.5-sonnet", "sk-or-test-secret", 32768, 0)
	if _, err := c.Chat(context.Background(), "s", "u"); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if gotAuth != "Bearer sk-or-test-secret" {
		t.Fatalf("Authorization = %q, want Bearer sk-or-test-secret", gotAuth)
	}
	if _, present := gotBody["options"]; present {
		t.Fatalf("options field should NOT be present when using real API key: %+v", gotBody)
	}
}
