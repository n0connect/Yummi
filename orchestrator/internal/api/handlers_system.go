package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/n0connect/yummi/orchestrator/internal/executor"
	"github.com/n0connect/yummi/orchestrator/internal/gateway"
	"github.com/n0connect/yummi/orchestrator/internal/sandbox"
	"github.com/n0connect/yummi/orchestrator/internal/session"
)

// HandleGatewayStatus reports the trust-boundary daemon's current state.
// Returns a degraded response rather than an error when gateway is unavailable,
// so the UI can display the connectivity state without 5xx noise.
func HandleGatewayStatus(pc *gateway.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if pc == nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"level":                    0,
				"rules_active":             false,
				"policy_kernel_consistent": false,
				"enforcement_generation":   0,
				"status":                   "not_configured",
			})
			return
		}

		status, err := pc.GetStatus()
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"level":                    0,
				"rules_active":             false,
				"policy_kernel_consistent": false,
				"enforcement_generation":   0,
				"status":                   "disconnected",
				"error":                    err.Error(),
			})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"level":                    status.Level,
			"rules_active":             status.RulesActive,
			"uptime_secs":              status.UptimeSecs,
			"policy_kernel_consistent": status.PolicyKernelConsistent,
			"enforcement_generation":   status.EnforcementGeneration,
			"last_reconcile_error":     status.LastReconcileError,
			"status":                   "connected",
		})
	}
}

func HandleSandboxStatus(bridge *sandbox.Bridge) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if bridge == nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"container": "",
				"status":    "not_configured",
			})
			return
		}

		err := bridge.Ping(r.Context())
		status := "healthy"
		if err != nil {
			status = "unreachable"
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"container": bridge.ContainerName(),
			"status":    status,
		})
	}
}

// HandleSandboxUpload copies one operator-supplied file onto the sandbox
// desktop (bridge.UploadFile's desktopDir) — e.g. a VPN config or a private
// key the operator wants available to tools running inside the sandbox.
// Deliberately NOT routed through executor.RunScopedCommand/gateway: this
// never touches the network, local host-to-sandbox filesystem copy only, the
// same reasoning sandboxmcp.NeedsTarget already applies to pure local-file
// MCP tools (checksec_audit, consolidate_hosts, ...). multipart/form-data,
// field name "file" (optional "session_id" field for audit attribution);
// size is bounded by BodyLimit's existing 8 MiB cap.
//
// Every other side-effecting operator action in this codebase leaves an
// audit trail (the gateway's own hash-chained log for scope/rule decisions,
// appendAuditSafe for a blocked sandbox/exec) — a silent file landing in the
// sandbox would be the one exception. Logged on both outcomes (sm may be
// nil, e.g. in tests exercising the handler directly — appendAuditSafe
// degrades to a no-op rather than panicking, same as every other caller).
func HandleSandboxUpload(bridge *sandbox.Bridge, sm *session.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if bridge == nil {
			writeError(w, http.StatusServiceUnavailable, "sandbox bridge not configured", nil)
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, "file field is required", err)
			return
		}
		defer func() { _ = file.Close() }()
		sessionID := normalizeSessionID(r.FormValue("session_id"))

		safeName, err := sandbox.SanitizeUploadFilename(header.Filename)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid filename", err)
			return
		}
		if err := bridge.UploadFile(r.Context(), safeName, file); err != nil {
			appendAuditSafe(sm, sessionID, "sandbox.upload.failed", "operator", map[string]any{
				"filename": safeName, "size": header.Size, "error": err.Error(),
			})
			writeError(w, http.StatusBadGateway, "upload to sandbox failed", err)
			return
		}
		appendAuditSafe(sm, sessionID, "sandbox.upload", "operator", map[string]any{
			"filename": safeName, "size": header.Size,
		})
		writeJSON(w, http.StatusOK, map[string]any{"filename": safeName, "status": "uploaded"})
	}
}

// HandleSandboxExec runs a single command in the sandbox Kali container.
// All commands pass through a gateway scope check before execution; fail-closed
// if gateway is unavailable.
func HandleSandboxExec(bridge *sandbox.Bridge, pc *gateway.Client, sm *session.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if bridge == nil {
			writeError(w, http.StatusServiceUnavailable, "sandbox bridge not configured", nil)
			return
		}

		var req struct {
			Command   string `json:"command"`
			SessionID string `json:"session_id,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request", err)
			return
		}
		if req.Command == "" {
			writeError(w, http.StatusBadRequest, "command is required", nil)
			return
		}

		sessionID := normalizeSessionID(req.SessionID)

		// All scope decisions and the nftables unlock happen inside
		// RunScopedCommand — gateway is the sole authority. This is fail-closed:
		// if gateway is unavailable, the command is rejected.
		result, err := executor.RunScopedCommand(r.Context(), bridge, pc, req.Command)
		if err != nil {
			if errors.Is(err, executor.ErrOutOfScope) {
				appendAuditSafe(sm, sessionID, "sandbox.exec.blocked", "gateway", map[string]any{
					"command": req.Command,
					"error":   err.Error(),
				})
				writeError(w, http.StatusForbidden, "target is not in scope", nil)
				return
			}
			appendAuditSafe(sm, sessionID, "sandbox.exec.blocked", "gateway", map[string]any{
				"command": req.Command,
				"error":   err.Error(),
			})
			writeError(w, http.StatusServiceUnavailable, "gateway scope check unavailable", err)
			return
		}

		appendAuditSafe(sm, sessionID, "sandbox.exec.completed", "orchestrator", map[string]any{
			"command":     req.Command,
			"exit_code":   result.ExitCode,
			"duration_ms": result.Duration,
		})

		resp := map[string]any{
			"exit_code":   result.ExitCode,
			"stdout":      result.Stdout,
			"stderr":      result.Stderr,
			"duration_ms": result.Duration,
		}
		if result.Warning != "" {
			resp["warning"] = result.Warning
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// HandleGetWebSearch reports whether web research is currently enabled.
// GET /api/v1/system/web-search
func HandleGetWebSearch(bridge *sandbox.Bridge) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if bridge == nil {
			writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
			return
		}
		res, err := bridge.Exec(r.Context(), "cat /tmp/yummi_web_search_state 2>/dev/null || echo ${YUMMI_WEB_SEARCH_ENABLED:-false}")
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
			return
		}
		val := strings.ToLower(strings.TrimSpace(res.Stdout))
		enabled := val == "true" || val == "1" || val == "enabled" || val == "on"
		writeJSON(w, http.StatusOK, map[string]any{"enabled": enabled})
	}
}

// HandleSetWebSearch updates whether web research is dynamically enabled in sandbox.
// POST /api/v1/system/web-search {"enabled": true|false}
func HandleSetWebSearch(bridge *sandbox.Bridge, sm *session.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if bridge == nil {
			writeError(w, http.StatusServiceUnavailable, "sandbox unavailable", nil)
			return
		}
		var req struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request", err)
			return
		}
		cmd := "echo false > /tmp/yummi_web_search_state"
		if req.Enabled {
			cmd = "echo true > /tmp/yummi_web_search_state"
		}
		if _, err := bridge.Exec(r.Context(), cmd); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update web search state", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"enabled": req.Enabled})
	}
}
