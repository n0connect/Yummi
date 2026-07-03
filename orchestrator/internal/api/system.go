package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/n0connect/yummi/orchestrator/internal/conductor"
	"github.com/n0connect/yummi/orchestrator/internal/gateway"
	"github.com/n0connect/yummi/orchestrator/internal/sandbox"
)

// ─── Log Ring Buffer ───────────────────────────────────────────────────────────

const logRingSize = 500

// LogEntry is a single structured log record for the debug panel.
type LogEntry struct {
	Time    string         `json:"t"`
	Level   string         `json:"level"`
	Message string         `json:"msg"`
	Source  string         `json:"src,omitempty"`
	Attrs   map[string]any `json:"attrs,omitempty"`
}

// LogBuffer is a thread-safe ring buffer that captures slog records and fans
// out to SSE subscribers.
type LogBuffer struct {
	mu      sync.RWMutex
	ring    [logRingSize]LogEntry
	head    int // next write position
	count   int // total entries (capped at logRingSize)
	subs    map[int]chan LogEntry
	nextSub int
	parent  slog.Handler
}

// NewLogBuffer creates a LogBuffer that wraps a parent slog.Handler.
func NewLogBuffer(parent slog.Handler) *LogBuffer {
	return &LogBuffer{
		parent: parent,
		subs:   make(map[int]chan LogEntry),
	}
}

func (lb *LogBuffer) Enabled(ctx context.Context, level slog.Level) bool {
	return lb.parent.Enabled(ctx, level)
}

func (lb *LogBuffer) Handle(ctx context.Context, r slog.Record) error {
	entry := LogEntry{
		Time:    r.Time.UTC().Format(time.RFC3339),
		Level:   r.Level.String(),
		Message: r.Message,
		Source:  sourceFromRecord(r),
		Attrs:   make(map[string]any),
	}
	r.Attrs(func(a slog.Attr) bool {
		entry.Attrs[a.Key] = a.Value.Any()
		return true
	})
	lb.push(entry)
	return lb.parent.Handle(ctx, r)
}

func (lb *LogBuffer) WithAttrs(attrs []slog.Attr) slog.Handler {
	// Update parent handler with attributes and return self.
	// LogBuffer acts as a singleton wrapper - all child handlers
	// created via WithAttrs/WithGroup log to the same ring buffer.
	// This avoids race conditions from shallow copies while maintaining
	// proper attribute/group context in the parent handler.
	lb.mu.Lock()
	lb.parent = lb.parent.WithAttrs(attrs)
	lb.mu.Unlock()
	return lb
}

func (lb *LogBuffer) WithGroup(name string) slog.Handler {
	// Same singleton pattern as WithAttrs
	lb.mu.Lock()
	lb.parent = lb.parent.WithGroup(name)
	lb.mu.Unlock()
	return lb
}

func (lb *LogBuffer) push(e LogEntry) {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	lb.ring[lb.head] = e
	lb.head = (lb.head + 1) % logRingSize
	if lb.count < logRingSize {
		lb.count++
	}
	for _, ch := range lb.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

// Snapshot returns the current ring buffer contents in chronological order.
func (lb *LogBuffer) Snapshot() []LogEntry {
	lb.mu.RLock()
	defer lb.mu.RUnlock()
	if lb.count == 0 {
		return nil
	}
	out := make([]LogEntry, lb.count)
	if lb.count < logRingSize {
		copy(out, lb.ring[:lb.count])
		return out
	}
	// full ring — oldest entry is at head
	n := copy(out, lb.ring[lb.head:])
	copy(out[n:], lb.ring[:lb.head])
	return out
}

// Subscribe registers a channel to receive live log entries. Caller must call
// Unsubscribe with the returned key when done.
func (lb *LogBuffer) Subscribe() (int, <-chan LogEntry) {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	id := lb.nextSub
	lb.nextSub++
	ch := make(chan LogEntry, 64)
	lb.subs[id] = ch
	return id, ch
}

// Unsubscribe removes a subscriber.
func (lb *LogBuffer) Unsubscribe(id int) {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	if ch, ok := lb.subs[id]; ok {
		close(ch)
		delete(lb.subs, id)
	}
}

func sourceFromRecord(r slog.Record) string {
	if r.PC == 0 {
		return ""
	}
	// Use program counter to get a short source location
	// This is best-effort and avoids importing runtime/debug
	return ""
}

// ─── System Status ─────────────────────────────────────────────────────────────

// ModuleStatus represents the health of a single system module.
type ModuleStatus struct {
	Name       string         `json:"name"`
	Status     string         `json:"status"` // healthy | degraded | down | unknown
	Details    string         `json:"details,omitempty"`
	UptimeSecs *int64         `json:"uptime_secs,omitempty"`
	Meta       map[string]any `json:"meta,omitempty"`
}

// SystemStatusResponse is the response for GET /api/v1/system/status.
type SystemStatusResponse struct {
	Overall   string         `json:"overall"` // healthy | degraded | down
	Modules   []ModuleStatus `json:"modules"`
	CheckedAt string         `json:"checked_at"`
}

var orchestratorStartTime = time.Now()

// HandleSystemStatus aggregates health from all system modules.
func HandleSystemStatus(bridge *sandbox.Bridge, pc *gateway.Client, dockerSocket string, engine *conductor.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
		defer cancel()

		modules := make([]ModuleStatus, 0, 4)

		// ── sandbox ─────────────────────────────────────────────────────────
		sandboxMod := ModuleStatus{Name: "sandbox"}
		if bridge == nil {
			sandboxMod.Status = "not_configured"
			sandboxMod.Details = "bridge not initialized"
		} else {
			sandboxMod.Meta = map[string]any{"container": bridge.ContainerName()}
			if err := bridge.Ping(ctx); err != nil {
				sandboxMod.Status = "down"
				sandboxMod.Details = "container unreachable"
			} else {
				sandboxMod.Status = "healthy"
				sandboxMod.Details = "kali container responding"
			}
		}
		modules = append(modules, sandboxMod)

		// ── gateway ─────────────────────────────────────────────────────────
		gatewayMod := ModuleStatus{Name: "gateway"}
		if pc == nil {
			gatewayMod.Status = "not_configured"
			gatewayMod.Details = "trust boundary not initialized"
		} else if gst, err := pc.GetStatus(); err != nil {
			gatewayMod.Status = "down"
			gatewayMod.Details = "IPC unreachable"
		} else {
			gatewayMod.Status = "healthy"
			gatewayMod.Details = "trust boundary active"
			up := int64(gst.UptimeSecs)
			gatewayMod.UptimeSecs = &up
			gatewayMod.Meta = map[string]any{
				"level":        gst.Level,
				"rules_active": gst.RulesActive,
			}
		}
		modules = append(modules, gatewayMod)

		// ── orchestrator self ────────────────────────────────────────────────
		orchestratorUptime := int64(time.Since(orchestratorStartTime).Seconds())
		hostname, _ := os.Hostname()
		orchestratorMod := ModuleStatus{
			Name:       "orchestrator",
			Status:     "healthy",
			Details:    "orchestrator running",
			UptimeSecs: &orchestratorUptime,
			Meta:       map[string]any{"host": hostname, "version": "v0.1.0"},
		}
		modules = append(modules, orchestratorMod)

		// ── conductor ───────────────────────────────────────────────────────
		conductorMod := ModuleStatus{Name: "conductor"}
		if engine == nil {
			conductorMod.Status = "not_configured"
			conductorMod.Details = "conductor engine not loaded"
			conductorMod.Meta = map[string]any{"backend": "none"}
		} else if model := engine.OllamaModel(); model != "" {
			conductorMod.Status = "healthy"
			conductorMod.Details = "LLM backend connected"
			conductorMod.Meta = map[string]any{"backend": "ollama", "model": model}
		} else {
			conductorMod.Status = "degraded"
			conductorMod.Details = "rule engine only (Ollama not configured)"
			conductorMod.Meta = map[string]any{"backend": "rules", "persona": engine.PersonaVersion()}
		}
		modules = append(modules, conductorMod)

		// ── docker daemon ────────────────────────────────────────────────────
		dockerMod := checkDockerDaemon(dockerSocket)
		modules = append(modules, dockerMod)

		// ── overall ─────────────────────────────────────────────────────────
		overall := overallStatus(modules)

		writeJSON(w, http.StatusOK, SystemStatusResponse{
			Overall:   overall,
			Modules:   modules,
			CheckedAt: time.Now().UTC().Format(time.RFC3339),
		})
	}
}

// checkDockerDaemon probes Docker availability. When DOCKER_HOST is set to a
// tcp:// address — the hardened deployment's docker-compose.yml points the
// orchestrator at docker-proxy:2375 rather than mounting /var/run/docker.sock
// directly (see the docker-proxy service's CONTAINERS/EXEC/POST allowlist) —
// it dials that instead: the orchestrator container has no socket file to
// stat in that setup, so falling through to the socketPath check would always
// misreport "down" even though `docker` CLI calls (sandbox.Bridge.Exec) work
// fine through the proxy. socketPath remains the fallback for the
// no-DOCKER_HOST, direct-socket-mount case.
func checkDockerDaemon(socketPath string) ModuleStatus {
	if host := os.Getenv("DOCKER_HOST"); strings.HasPrefix(host, "tcp://") {
		return checkDockerTCPHost(strings.TrimPrefix(host, "tcp://"))
	}
	mod := ModuleStatus{Name: "docker"}
	if socketPath == "" {
		mod.Status = "not_configured"
		mod.Details = "docker socket path not set"
		return mod
	}
	if _, err := os.Stat(socketPath); err != nil {
		mod.Status = "down"
		mod.Details = "socket not found: " + socketPath
		return mod
	}
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		mod.Status = "down"
		mod.Details = "socket unreachable: " + err.Error()
		return mod
	}
	_ = conn.Close()
	mod.Status = "healthy"
	mod.Details = "daemon responding"
	mod.Meta = map[string]any{"socket": socketPath}
	return mod
}

func checkDockerTCPHost(hostPort string) ModuleStatus {
	mod := ModuleStatus{Name: "docker"}
	conn, err := net.DialTimeout("tcp", hostPort, 2*time.Second)
	if err != nil {
		mod.Status = "down"
		mod.Details = "docker proxy unreachable: " + err.Error()
		return mod
	}
	_ = conn.Close()
	mod.Status = "healthy"
	mod.Details = "daemon responding (via docker-proxy)"
	mod.Meta = map[string]any{"docker_host": hostPort}
	return mod
}

func overallStatus(modules []ModuleStatus) string {
	hasDown := false
	hasDegraded := false
	for _, m := range modules {
		switch m.Status {
		case "down":
			hasDown = true
		case "degraded":
			hasDegraded = true
		}
	}
	if hasDown {
		return "degraded"
	}
	if hasDegraded {
		return "degraded"
	}
	return "healthy"
}

// ─── System Logs SSE ───────────────────────────────────────────────────────────

// HandleSystemLogs streams log entries to the client via Server-Sent Events.
// On connection it replays the ring buffer snapshot, then streams live entries.
func HandleSystemLogs(lb *LogBuffer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		w.Header().Set("Connection", "keep-alive")

		sendEntry := func(e LogEntry) bool {
			data, err := json.Marshal(e)
			if err != nil {
				return false
			}
			_, writeErr := fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
			return writeErr == nil
		}

		// Replay snapshot
		for _, e := range lb.Snapshot() {
			if !sendEntry(e) {
				return
			}
		}

		// Live stream
		subID, ch := lb.Subscribe()
		defer lb.Unsubscribe(subID)

		for {
			select {
			case <-r.Context().Done():
				return
			case e, ok := <-ch:
				if !ok {
					return
				}
				if !sendEntry(e) {
					return
				}
			}
		}
	}
}
