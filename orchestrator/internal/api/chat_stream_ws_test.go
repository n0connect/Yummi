package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/n0connect/yummi/orchestrator/internal/conductor"
	"github.com/n0connect/yummi/orchestrator/internal/execution"
)

// newNDJSONOllamaServer is a real httptest.Server (not a mock) answering
// /api/chat with a streamed NDJSON response — one line per chunk, then a
// final done:true line. Mirrors the fake server pattern used in
// internal/llm and internal/conductor's own streaming tests.
func newNDJSONOllamaServer(t *testing.T, chunks ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, _ := w.(http.Flusher)
		for _, c := range chunks {
			line, _ := json.Marshal(map[string]any{
				"message": map[string]string{"role": "assistant", "content": c},
			})
			_, _ = w.Write(append(line, '\n'))
			if flusher != nil {
				flusher.Flush()
			}
		}
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":""},"done":true}` + "\n"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestHandleEventsWSDeliversChatStreamDeltas proves the end-to-end wiring:
// a real WebSocket client connected to /ws/events sees "chat.stream" delta
// events (and a terminal done event) while a real HandleChat request is in
// flight against a streaming fake Ollama backend, with the REST response
// still landing separately and unaffected by streaming.
func TestHandleEventsWSDeliversChatStreamDeltas(t *testing.T) {
	sm := openTestManager(t)
	ollama := newNDJSONOllamaServer(t, `{"intent":"expl`, `ain","message":"streamed hello","confidence":0.9}`)

	engine, err := conductor.NewEngine(t.TempDir(), ollama.URL, "test-model")
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	executions := execution.NewStore()

	wsServer := httptest.NewServer(HandleEventsWS(executions, nil, nil, nil, engine))
	t.Cleanup(wsServer.Close)

	wsURL := "ws" + strings.TrimPrefix(wsServer.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	// Drain messages off the socket into a channel so the chat call below
	// (which runs concurrently on this same test goroutine) doesn't race the
	// reader.
	type wsMsg struct {
		Type  string `json:"type"`
		Event string `json:"event"`
		Data  struct {
			SessionID string `json:"session_id"`
			Delta     string `json:"delta"`
			Done      bool   `json:"done"`
		} `json:"data"`
	}
	msgs := make(chan wsMsg, 32)
	go func() {
		for {
			var m wsMsg
			if err := conn.ReadJSON(&m); err != nil {
				close(msgs)
				return
			}
			msgs <- m
		}
	}()

	chat := HandleChat(engine, conductor.NewApprovalStore(0), sm, nil, nil, 90*time.Second)
	body := `{"message":"hello","session_id":"default"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	chat.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("chat: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var got strings.Builder
	sawDone := false
	deadline := time.After(3 * time.Second)
collect:
	for {
		select {
		case m, ok := <-msgs:
			if !ok {
				break collect
			}
			if m.Type != "chat.stream" {
				continue
			}
			if m.Data.SessionID != "default" {
				t.Fatalf("unexpected session id on chat.stream event: %+v", m)
			}
			if m.Data.Done {
				if m.Event != "chat.done" {
					t.Fatalf("expected event=chat.done on the terminal event, got %q", m.Event)
				}
				sawDone = true
				break collect
			}
			if m.Event != "chat.delta" {
				t.Fatalf("expected event=chat.delta on a non-terminal event, got %q", m.Event)
			}
			got.WriteString(m.Data.Delta)
		case <-deadline:
			break collect
		}
	}

	if !sawDone {
		t.Fatal("never observed a terminal chat.stream done=true event over the websocket")
	}
	// The model's raw output is the whole {"intent":...,"message":"...",...}
	// JSON object, not prose — the delta stream carries only the extracted
	// "message" text (see conductor.extractMessagePreview), never the
	// surrounding JSON structure, or an operator would briefly see raw JSON
	// syntax in the live "typing" preview before the REST reply replaces it.
	want := "streamed hello"
	if got.String() != want {
		t.Fatalf("streamed deltas did not reassemble to the extracted message: got %q want %q", got.String(), want)
	}

	// The REST response is the untouched source of truth for the parsed
	// ChatResponse, regardless of what streamed over the websocket.
	var restResp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &restResp); err != nil {
		t.Fatalf("decode chat response: %v", err)
	}
	if restResp["reply"] != "streamed hello" {
		t.Fatalf("expected REST reply 'streamed hello', got %#v", restResp["reply"])
	}
}
