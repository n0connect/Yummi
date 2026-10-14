//! Terminal WebSocket bridge — PTY-backed docker exec.
//
// Protocol:
//   WebSocket binary frame  → raw PTY I/O (keystrokes → container stdin)
//   WebSocket text frame    → JSON control message (resize, ping)
//   Server → client binary  → PTY output (ANSI escape codes preserved)
//
// Using creack/pty allocates a real pseudo-terminal in the orchestrator container, which enables
// full-screen TUI apps (vim, htop, nano) and correct ANSI colour rendering.

package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os/exec"
	"time"

	"github.com/creack/pty"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/n0connect/yummi/orchestrator/internal/sandbox"
)

// Each connection owns only its tmux client, never the persistent shell.
// Docker CLI cancellation alone can leave the container-side client attached.
const terminalAttachScript = `set -eu
umask 077
printf '%s\n' "$$" > "$1"
exec tmux new-session -A -s operator`

const terminalDetachScript = `set -eu
for attempt in 1 2 3 4 5 6 7 8 9 10; do
  [ -f "$1" ] && break
  sleep 0.1
done
[ -f "$1" ] || exit 0
read -r attachment_pid < "$1"
case "$attachment_pid" in ''|*[!0-9]*) exit 1 ;; esac
tmux list-clients -t operator -F '#{client_pid} #{client_tty}' | while read -r candidate tty; do
  if [ "$candidate" = "$attachment_pid" ]; then
    tmux detach-client -t "$tty"
  fi
done
rm -- "$1"`

const (
	terminalOutputChunkSize = 4096
	terminalPingInterval    = 20 * time.Second
	terminalWriteTimeout    = 5 * time.Second
	terminalReadLimitBytes  = 64 * 1024
)

// terminalControlMsg is the JSON structure sent in WebSocket text frames.
// Binary frames carry raw PTY bytes; text frames carry control commands.
type terminalControlMsg struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
}

// PTY-backed interactive shell inside the sandbox container.
func HandleTerminalWSBridge(bridge *sandbox.Bridge) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if bridge == nil {
			http.Error(w, "sandbox bridge not configured", http.StatusServiceUnavailable)
			return
		}

		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			slog.Warn("terminal ws: upgrade failed", "error", err)
			return
		}
		defer func() { _ = conn.Close() }()

		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		// docker exec -it allocates a TTY inside the container; pty.Start()
		// provides the matching PTY in the orchestrator container so ANSI codes flow correctly.
		//
		// `tmux new-session -A -s operator` (attach if it exists, create if
		// not) instead of a bare shell: every WS connection — a page refresh,
		// a dropped connection, a new browser tab — reattaches to the SAME
		// session instead of starting a fresh, empty shell. A long-running
		// command, scrollback, and any other shell (vim, htop, a second pane)
		// the operator had open all survive a reconnect. Without this, the
		// Bash tab was a disposable shell: the exact "cut off from
		// everything, starts from zero every time" complaint that made it
		// feel like a toy next to a real terminal.
		attachmentPath := "/tmp/yummi-operator-" + uuid.NewString() + ".pid"
		cmd := exec.CommandContext(ctx, "docker", "exec", "-it",
			"--user", "1000:1000",
			bridge.ContainerName(),
			"/bin/sh", "-c", terminalAttachScript, "yummi-operator-terminal", attachmentPath,
		)

		ptyFile, err := pty.Start(cmd)
		if err != nil {
			slog.Error("terminal ws: pty start failed", "error", err)
			_ = conn.WriteMessage(websocket.TextMessage, []byte("Failed to start terminal: "+err.Error()))
			return
		}
		defer func() {
			_ = ptyFile.Close()
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cleanupCancel()
			cleanup := exec.CommandContext(cleanupCtx, "docker", "exec", "--user", "1000:1000",
				bridge.ContainerName(), "/bin/sh", "-c", terminalDetachScript, "yummi-operator-terminal", attachmentPath)
			if err := cleanup.Run(); err != nil {
				slog.Warn("terminal ws: own client cleanup failed", "error", err)
			}
		}()

		// PTY → WebSocket: stream binary frames as they arrive.
		go func() {
			buf := make([]byte, terminalOutputChunkSize)
			for {
				n, err := ptyFile.Read(buf)
				if n > 0 {
					_ = conn.SetWriteDeadline(time.Now().Add(terminalWriteTimeout))
					if werr := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
						cancel()
						return
					}
				}
				if err != nil {
					cancel()
					return
				}
			}
		}()

		// WebSocket → PTY: binary frames are raw input; text frames are JSON control.
		go func() {
			conn.SetReadLimit(terminalReadLimitBytes)
			for {
				msgType, msg, err := conn.ReadMessage()
				if err != nil {
					cancel()
					return
				}
				switch msgType {
				case websocket.BinaryMessage:
					if _, err := ptyFile.Write(msg); err != nil {
						cancel()
						return
					}
				case websocket.TextMessage:
					var ctrl terminalControlMsg
					if err := json.Unmarshal(msg, &ctrl); err == nil && ctrl.Type == "resize" && ctrl.Cols > 0 && ctrl.Rows > 0 {
						_ = pty.Setsize(ptyFile, &pty.Winsize{Rows: ctrl.Rows, Cols: ctrl.Cols})
					}
				}
			}
		}()

		// Wait for process exit or context cancellation, sending heartbeat pings.
		cmdDone := make(chan error, 1)
		go func() { cmdDone <- cmd.Wait() }()

		pingTicker := time.NewTicker(terminalPingInterval)
		defer pingTicker.Stop()

		for {
			select {
			case <-pingTicker.C:
				_ = conn.SetWriteDeadline(time.Now().Add(terminalWriteTimeout))
				if err := conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(terminalWriteTimeout)); err != nil {
					return
				}
			case exitErr := <-cmdDone:
				if exitErr != nil && ctx.Err() == nil {
					msg := "\r\n[terminal exited: " + exitErr.Error() + "]\r\n"
					_ = conn.SetWriteDeadline(time.Now().Add(terminalWriteTimeout))
					_ = conn.WriteMessage(websocket.BinaryMessage, []byte(msg))
				}
				return
			case <-ctx.Done():
				return
			}
		}
	}
}
