package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTimeoutUnlessWebSocketWrapsNormalRequestsWithDeadline(t *testing.T) {
	mw := TimeoutUnlessWebSocket(10 * time.Millisecond)
	var hasDeadline bool
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hasDeadline = r.Context().Deadline()
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/slow", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if !hasDeadline {
		t.Fatal("expected normal request context to have a timeout deadline")
	}
}

func TestCORSOnlyGrantsLocalhostOrigins(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := CORSMiddleware(next)

	cases := []struct {
		origin    string
		wantGrant bool
	}{
		{"http://localhost:3000", true},
		{"http://127.0.0.1:9080", true},
		{"https://evil.example.com", false},
		{"http://localhost.evil.com", false}, // suffix trick must not pass
		{"null", false},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
		req.Header.Set("Origin", tc.origin)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		got := w.Header().Get("Access-Control-Allow-Origin")
		if tc.wantGrant && got != tc.origin {
			t.Errorf("origin %q: expected ACAO %q, got %q", tc.origin, tc.origin, got)
		}
		if !tc.wantGrant && got != "" {
			t.Errorf("origin %q: expected NO CORS grant, got ACAO %q", tc.origin, got)
		}
	}
}

func TestTimeoutUnlessWebSocketSkipsUpgradeRequests(t *testing.T) {
	mw := TimeoutUnlessWebSocket(10 * time.Millisecond)
	var hasDeadline bool
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hasDeadline = r.Context().Deadline()
		w.WriteHeader(http.StatusSwitchingProtocols)
	}))

	req := httptest.NewRequest(http.MethodGet, "/ws/terminal", nil)
	req.Header.Set("Connection", "keep-alive, Upgrade")
	req.Header.Set("Upgrade", "websocket")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusSwitchingProtocols {
		t.Fatalf("expected websocket request to bypass timeout, got %d", w.Code)
	}
	if hasDeadline {
		t.Fatal("expected websocket upgrade request to bypass timeout deadline")
	}
}
