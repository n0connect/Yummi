package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStopTaskHitsStopEndpoint(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	if err := c.StopTask("t-7"); err != nil {
		t.Fatalf("StopTask: %v", err)
	}
	if gotPath != "/api/v1/tasks/t-7/stop" || gotMethod != http.MethodPost {
		t.Fatalf("wrong stop call: %s %s", gotMethod, gotPath)
	}
}

func TestResumeTaskHitsStartEndpoint(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	if err := c.ResumeTask("t-7"); err != nil {
		t.Fatalf("ResumeTask: %v", err)
	}
	if gotPath != "/api/v1/tasks/t-7/start" || gotMethod != http.MethodPost {
		t.Fatalf("wrong resume call: %s %s", gotMethod, gotPath)
	}
}

func TestApprovalModeReadsMode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/agent/mode" || r.Method != http.MethodGet {
			t.Errorf("wrong mode-get call: %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"mode":"auto"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	mode, err := c.ApprovalMode()
	if err != nil {
		t.Fatalf("ApprovalMode: %v", err)
	}
	if mode != "auto" {
		t.Fatalf("mode not read: %q", mode)
	}
}

func TestSetApprovalModeSendsMode(t *testing.T) {
	var body map[string]string
	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	if err := c.SetApprovalMode("ask_permission"); err != nil {
		t.Fatalf("SetApprovalMode: %v", err)
	}
	if gotMethod != http.MethodPost || body["mode"] != "ask_permission" {
		t.Fatalf("wrong set-mode call: %s body=%v", gotMethod, body)
	}
}
