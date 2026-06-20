package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestJoinURL(t *testing.T) {
	got, err := joinURL("http://127.0.0.1:8080", "/api/v1/sandbox/status")
	if err != nil {
		t.Fatalf("join url: %v", err)
	}
	want := "http://127.0.0.1:8080/api/v1/sandbox/status"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestWaitUntilReadySuccess(t *testing.T) {
	h := http.NewServeMux()
	h.HandleFunc("/api/v1/sandbox/status", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "healthy"})
	})
	h.HandleFunc("/api/v1/gateway/status", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "connected"})
	})
	srv := httptest.NewServer(h)
	defer srv.Close()

	if err := waitUntilReady(srv.URL, 2*time.Second, nil); err != nil {
		t.Fatalf("expected readiness success, got %v", err)
	}
}

func TestWaitUntilReadyFailure(t *testing.T) {
	h := http.NewServeMux()
	h.HandleFunc("/api/v1/sandbox/status", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "unreachable"})
	})
	h.HandleFunc("/api/v1/gateway/status", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "disconnected"})
	})
	srv := httptest.NewServer(h)
	defer srv.Close()

	if err := waitUntilReady(srv.URL, 500*time.Millisecond, nil); err == nil {
		t.Fatal("expected readiness failure")
	}
}
