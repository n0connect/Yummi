// Desktop WebSocket bridge — proxies the browser's noVNC (RFB.js) client
// through to the sandbox container's own websockify bridge. This is the
// SAME container the agent execs into (sandbox.Bridge / sandboxmcp.Client)
// and the operator's Bash tab attaches a tmux session on — one unified
// environment, this handler just opens a second, independent path into it.
//
// Unlike the terminal bridge (HandleTerminalWSBridge, terminal.go), this is
// NOT a docker-exec-backed PTY — sandbox runs its own persistent VNC server
// + websockify process (see sandbox/entrypoint.sh), and this handler is a
// pure byte relay between the browser's WebSocket connection and a second
// WebSocket connection this orchestrator dials to that container. Keeps the
// same trust-boundary shape every other browser-facing surface here
// follows: the browser only ever talks to orchestrator, never directly to a
// sandbox container's own network address.

package api

import (
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	desktopWriteTimeout = 5 * time.Second
	desktopPingInterval = 20 * time.Second
)

// desktopDialer intentionally omits an origin/TLS config — the upstream
// target is always the sandbox container on the internal Docker network,
// never operator-supplied.
var desktopDialer = websocket.Dialer{
	HandshakeTimeout: 10 * time.Second,
}

// HandleDesktopWSBridge relays the browser's noVNC WebSocket to
// ws://<desktopAddr>/ (sandbox's own websockify bridge, see Dockerfile's
// EXPOSE 6080 and entrypoint.sh). desktopAddr is host:port, e.g.
// "sandbox:6080" — resolved via Docker DNS on yummi_internal, the same
// network orchestrator already shares with every other component it
// reaches this way.
func HandleDesktopWSBridge(desktopAddr string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if desktopAddr == "" {
			http.Error(w, "desktop sandbox not configured", http.StatusServiceUnavailable)
			return
		}

		clientConn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			slog.Warn("desktop ws: client upgrade failed", "error", err)
			return
		}
		defer func() { _ = clientConn.Close() }()

		upstreamConn, _, err := desktopDialer.Dial("ws://"+desktopAddr+"/", nil)
		if err != nil {
			slog.Error("desktop ws: upstream dial failed", "addr", desktopAddr, "error", err)
			_ = clientConn.WriteMessage(websocket.TextMessage, []byte("desktop sandbox unreachable: "+err.Error()))
			return
		}
		defer func() { _ = upstreamConn.Close() }()

		var once sync.Once
		closeBoth := func() {
			_ = clientConn.Close()
			_ = upstreamConn.Close()
		}

		relay := func(dst, src *websocket.Conn) {
			defer once.Do(closeBoth)
			for {
				msgType, msg, err := src.ReadMessage()
				if err != nil {
					return
				}
				_ = dst.SetWriteDeadline(time.Now().Add(desktopWriteTimeout))
				if err := dst.WriteMessage(msgType, msg); err != nil {
					return
				}
			}
		}

		go relay(upstreamConn, clientConn)
		relay(clientConn, upstreamConn)
	}
}
