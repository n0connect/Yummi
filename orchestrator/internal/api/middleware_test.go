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
