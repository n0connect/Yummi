package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/n0connect/yummi/orchestrator/internal/conductor"
	"github.com/n0connect/yummi/orchestrator/internal/session"
)

func TestHandleChatAllowsDefaultSessionSentinel(t *testing.T) {
	sm := openTestManager(t)

	engine, err := conductor.NewEngine(t.TempDir(), "", "")
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}

	h := HandleChat(engine, conductor.NewApprovalStore(0), sm, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat", strings.NewReader(`{"message":"hedefi listele","session_id":"default"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for default session sentinel, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["session_id"] != "default" {
		t.Fatalf("expected session_id default, got %#v", resp["session_id"])
	}
}

func TestHandleChatEchoesClientTurnID(t *testing.T) {
	engine, err := conductor.NewEngine(t.TempDir(), "", "")
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	h := HandleChat(engine, conductor.NewApprovalStore(0), nil, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat", strings.NewReader(
		`{"message":"status","session_id":"default","turn_id":"f47ac10b-58cc-4372-a567-0e02b2c3d479"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		TurnID string `json:"turn_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.TurnID != "f47ac10b-58cc-4372-a567-0e02b2c3d479" {
		t.Fatalf("turn_id = %q", resp.TurnID)
	}
}

// TestChatPersistsTurnsAndMessagesEndpointReplaysThem proves the durable
// chat-history round-trip: a chat against a real session writes the operator's
// message and the assistant's reply to chat_messages, and GET
// /sessions/{id}/messages hands them back in order for the UI to rehydrate.
func TestChatPersistsTurnsAndMessagesEndpointReplaysThem(t *testing.T) {
	sm := openTestManager(t)
	now := time.Now().Unix()
	sess := &session.Session{ID: "feed-beef-cafe", Name: "Recon", CreatedAt: now, UpdatedAt: now, Status: "active"}
	if err := sm.Create(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// No LLM → the engine declines, but the turn (user msg + assistant reply)
	// still persists for a real session.
	engine, err := conductor.NewEngine(t.TempDir(), "", "")
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	chat := HandleChat(engine, conductor.NewApprovalStore(0), sm, nil, nil)
	body := `{"message":"enumerate the host","session_id":"feed-beef-cafe"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	chat.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("chat: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Now read them back via the messages endpoint.
	r := chi.NewRouter()
	r.Get("/sessions/{id}/messages", HandleListMessages(sm))
	mreq := httptest.NewRequest(http.MethodGet, "/sessions/feed-beef-cafe/messages", nil)
	mw := httptest.NewRecorder()
	r.ServeHTTP(mw, mreq)
	if mw.Code != http.StatusOK {
		t.Fatalf("messages: expected 200, got %d: %s", mw.Code, mw.Body.String())
	}

	var msgs []session.ChatMessage
	if err := json.Unmarshal(mw.Body.Bytes(), &msgs); err != nil {
		t.Fatalf("decode messages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 persisted turns (user+assistant), got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "user" || msgs[0].Content != "enumerate the host" {
		t.Fatalf("user turn not persisted correctly: %+v", msgs[0])
	}
	if msgs[1].Role != "assistant" || msgs[1].Content == "" {
		t.Fatalf("assistant turn not persisted correctly: %+v", msgs[1])
	}
}

// TestChatDoesNotPersistFeedbackTurns proves auto-feedback turns (from_feedback)
// stay out of the durable thread so a reload doesn't replay synthetic
// tool-output as operator messages.
func TestChatDoesNotPersistFeedbackTurns(t *testing.T) {
	sm := openTestManager(t)
	now := time.Now().Unix()
	_ = sm.Create(&session.Session{ID: "dead-beef-feed", Name: "X", CreatedAt: now, UpdatedAt: now, Status: "active"})

	engine, _ := conductor.NewEngine(t.TempDir(), "", "")
	chat := HandleChat(engine, conductor.NewApprovalStore(0), sm, nil, nil)
	body := `{"message":"[KOMUT TAMAMLANDI] output...","session_id":"dead-beef-feed","from_feedback":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	chat.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("chat: expected 200, got %d", w.Code)
	}

	msgs, err := sm.ListMessages("dead-beef-feed", 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("from_feedback turn should not persist, got %d messages", len(msgs))
	}
}

func openTestManager(t *testing.T) *session.Manager {
	t.Helper()
	db, err := session.OpenDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return session.NewManager(db)
}

func TestHandleChatStartTaskIntentCreatesTask(t *testing.T) {
	created := ""
	hook := func(ctx context.Context, sessionID, goal string) (string, error) {
		created = goal
		return "task-123", nil
	}
	resp := applyStartTask(context.Background(), conductor.ChatResponse{
		Intent:   "start_task",
		Message:  "I will plan this",
		TaskGoal: "enumerate the host",
	}, "sess-1", hook)
	if created != "enumerate the host" {
		t.Fatalf("task not created, got %q", created)
	}
	if resp.TaskID != "task-123" {
		t.Fatalf("task id not propagated: %+v", resp)
	}
}
