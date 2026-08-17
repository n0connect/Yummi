package api

import (
	"encoding/json"
	"errors"
	"net/http"

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
