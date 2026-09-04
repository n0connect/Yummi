package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestDesktopHandlerRejectsEmptyAddr confirms the "desktop address not
// configured" case (SandboxDesktopAddr empty) degrades to a clean 503
// rather than attempting a dial that could hang or panic.
func TestDesktopHandlerRejectsEmptyAddr(t *testing.T) {
	handler := HandleDesktopWSBridge("")
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", resp.StatusCode)
	}
}

// TestDesktopHandlerRelaysBytesBothWays exercises the full relay path
// against a REAL local WebSocket server standing in for the sandbox
// container's websockify bridge — not a mock of orchestrator's own code, a
// genuine second WS endpoint the handler dials exactly as it would dial the
// real container. Confirms bytes sent by a "browser" client reach the "upstream"
// server, and the upstream's own binary replies come back to the client —
// the same shape RFB traffic needs in both directions.
func TestDesktopHandlerRelaysBytesBothWays(t *testing.T) {
	upgrader := websocket.Upgrader{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upstream upgrade: %v", err)
			return
		}
		defer conn.Close()
		for {
			mt, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			// Echo back with a marker so the test can distinguish "the
			// upstream really received and replied" from a coincidence.
			_ = conn.WriteMessage(mt, append([]byte("upstream-saw:"), msg...))
		}
	}))
	defer upstream.Close()
	upstreamAddr := strings.TrimPrefix(upstream.URL, "http://")

	bridge := httptest.NewServer(HandleDesktopWSBridge(upstreamAddr))
	defer bridge.Close()
	bridgeURL := "ws://" + strings.TrimPrefix(bridge.URL, "http://")

	clientConn, _, err := websocket.DefaultDialer.Dial(bridgeURL, nil)
	if err != nil {
		t.Fatalf("dial bridge: %v", err)
	}
	defer clientConn.Close()

	if err := clientConn.WriteMessage(websocket.BinaryMessage, []byte("rfb-hello")); err != nil {
		t.Fatalf("write: %v", err)
	}

	_ = clientConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, msg, err := clientConn.ReadMessage()
	if err != nil {
		t.Fatalf("read reply: %v", err)
	}
	if string(msg) != "upstream-saw:rfb-hello" {
		t.Fatalf("relay corrupted the round trip: got %q", msg)
	}
}

// TestDesktopHandlerUnreachableUpstream confirms a dial failure (upstream
// container down/not started) surfaces as a clean text message on the
// client connection instead of a hang or a panic.
func TestDesktopHandlerUnreachableUpstream(t *testing.T) {
	// A port nothing listens on.
	handler := HandleDesktopWSBridge("127.0.0.1:1")
	srv := httptest.NewServer(handler)
	defer srv.Close()
	wsURL := "ws://" + strings.TrimPrefix(srv.URL, "http://")

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("client dial (should upgrade fine even if upstream is down): %v", err)
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	mt, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("expected an error message frame, got read error: %v", err)
	}
	if mt != websocket.TextMessage || !strings.Contains(string(msg), "unreachable") {
		t.Fatalf("expected an 'unreachable' text message, got type=%d msg=%q", mt, msg)
	}
}
