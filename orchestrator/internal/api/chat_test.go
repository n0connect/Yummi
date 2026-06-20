package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

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
