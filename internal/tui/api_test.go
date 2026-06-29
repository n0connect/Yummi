package tui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateSessionReadsID(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte(`{"id":"sess-123","name":"TUI"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	if err := c.CreateSession(); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if gotPath != "/api/v1/sessions" {
		t.Fatalf("wrong path: %s", gotPath)
	}
	if c.SessionID != "sess-123" {
		t.Fatalf("session id not read from `id`: %q", c.SessionID)
	}
	var body map[string]string
	_ = json.Unmarshal([]byte(gotBody), &body)
	if body["name"] != "TUI" {
		t.Fatalf("create body should send name, got %s", gotBody)
	}
}

// Regression for the TUI/web personality-drift bug: chat history lives
// in-memory on the conductor keyed by session id, and the TUI previously
// called CreateSession() unconditionally on every launch — a fresh, empty
// history every time, unlike the web UI which reuses one session id for a
// whole browser tab. EnsureSession must reuse a still-valid persisted id
// instead of always minting a new one.
func TestEnsureSessionReusesPersistedID(t *testing.T) {
	t.Setenv("YUMMI_HOME", t.TempDir())
	savePersistedSessionID("sess-old")

	var gotPath string
	created := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.Method == http.MethodPost {
			created = true
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	if err := c.EnsureSession(); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	if c.SessionID != "sess-old" {
		t.Fatalf("expected reused session id, got %q", c.SessionID)
	}
	if gotPath != "/api/v1/sessions/sess-old" {
		t.Fatalf("expected a GET existence check, got path %q", gotPath)
	}
	if created {
		t.Fatal("EnsureSession should not create a new session when the persisted one is still valid")
	}
}

func TestEnsureSessionCreatesAndPersistsWhenNoneSaved(t *testing.T) {
	t.Setenv("YUMMI_HOME", t.TempDir())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"id":"sess-new","name":"TUI"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	if err := c.EnsureSession(); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	if c.SessionID != "sess-new" {
		t.Fatalf("expected the newly created session id, got %q", c.SessionID)
	}
	if got := loadPersistedSessionID(); got != "sess-new" {
		t.Fatalf("new session id not persisted for next launch, got %q", got)
	}
}

func TestEnsureSessionFallsBackWhenPersistedIDStale(t *testing.T) {
	t.Setenv("YUMMI_HOME", t.TempDir())
	savePersistedSessionID("sess-gone")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"id":"sess-new","name":"TUI"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound) // the orchestrator no longer knows "sess-gone"
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	if err := c.EnsureSession(); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	if c.SessionID != "sess-new" {
		t.Fatalf("expected fallback to a freshly created session, got %q", c.SessionID)
	}
}

func TestChatParsesProposalsAndTask(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/chat" {
			t.Errorf("wrong chat path: %s", r.URL.Path)
		}
		var req map[string]string
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["message"] == "" || req["session_id"] == "" {
			t.Errorf("chat must send message+session_id, got %v", req)
		}
		_, _ = w.Write([]byte(`{"reply":"running scan","session_id":"s1","data":{"intent":"suggest_command","proposals":[{"id":"p1","command":"nmap -sV 10.0.0.5","risk_level":"medium"}],"task_id":""}}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	c.SessionID = "s1"
	res, err := c.Chat("scan it")
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if res.Intent != "suggest_command" || len(res.Proposals) != 1 || res.Proposals[0].Command != "nmap -sV 10.0.0.5" {
		t.Fatalf("proposals not parsed: %+v", res)
	}
	if res.Reply != "running scan" {
		t.Fatalf("reply not parsed: %q", res.Reply)
	}
}

func TestChatStartTaskCarriesTaskID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"reply":"starting","session_id":"s1","data":{"intent":"start_task","task_id":"t-9","task_goal":"enumerate"}}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	c.SessionID = "s1"
	res, _ := c.Chat("enumerate the host")
	if res.Intent != "start_task" || res.TaskID != "t-9" {
		t.Fatalf("start_task not parsed: %+v", res)
	}
}

func TestApproveSendsProposalID(t *testing.T) {
	var body map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/chat/approve" {
			t.Errorf("wrong approve path: %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	c.SessionID = "s1"
	if err := c.Approve("p1"); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if body["proposal_id"] != "p1" || body["session_id"] != "s1" {
		t.Fatalf("approve body wrong: %v", body)
	}
}
