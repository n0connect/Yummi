package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/n0connect/yummi/orchestrator/internal/agent"
)

func TestHandleSetTaskAutoApprove_TogglesTheTaskField(t *testing.T) {
	store, err := agent.NewStore(nil)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	runner := agent.NewRunner(store, nil, nil, nil, agent.Config{})
	task, err := store.CreateSubtask("sess-1", "recon", 5, "")
	if err != nil {
		t.Fatalf("CreateSubtask: %v", err)
	}

	r := chi.NewRouter()
	r.Post("/tasks/{id}/auto-approve", HandleSetTaskAutoApprove(runner))

	req := httptest.NewRequest(http.MethodPost, "/tasks/"+task.ID+"/auto-approve", strings.NewReader(`{"enable":true}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]bool
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body["auto_approve_non_critical"] {
		t.Fatalf("response body = %+v, want auto_approve_non_critical=true", body)
	}

	got, ok := store.Get(task.ID)
	if !ok {
		t.Fatal("task vanished")
	}
	if !got.AutoApproveNonCritical {
		t.Fatal("expected AutoApproveNonCritical to be true on the stored task")
	}

	// Flip it back off.
	req2 := httptest.NewRequest(http.MethodPost, "/tasks/"+task.ID+"/auto-approve", strings.NewReader(`{"enable":false}`))
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec2.Code, rec2.Body.String())
	}
	got2, _ := store.Get(task.ID)
	if got2.AutoApproveNonCritical {
		t.Fatal("expected AutoApproveNonCritical to be false after disabling")
	}
}

func TestHandleSetTaskAutoApprove_UnknownTask(t *testing.T) {
	store, _ := agent.NewStore(nil)
	runner := agent.NewRunner(store, nil, nil, nil, agent.Config{})

	r := chi.NewRouter()
	r.Post("/tasks/{id}/auto-approve", HandleSetTaskAutoApprove(runner))

	req := httptest.NewRequest(http.MethodPost, "/tasks/does-not-exist/auto-approve", strings.NewReader(`{"enable":true}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404 for an unknown task", rec.Code)
	}
}

func TestHandleSetTaskAutoApprove_InvalidTaskIDFormat(t *testing.T) {
	store, _ := agent.NewStore(nil)
	runner := agent.NewRunner(store, nil, nil, nil, agent.Config{})

	r := chi.NewRouter()
	r.Post("/tasks/{id}/auto-approve", HandleSetTaskAutoApprove(runner))

	req := httptest.NewRequest(http.MethodPost, "/tasks/bad_id!/auto-approve", strings.NewReader(`{"enable":true}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400 for a malformed task id", rec.Code)
	}
}
