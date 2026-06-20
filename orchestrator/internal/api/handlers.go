package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/n0connect/yummi/orchestrator/internal/conductor"
	"github.com/n0connect/yummi/orchestrator/internal/execution"
	"github.com/n0connect/yummi/orchestrator/internal/session"
)

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     isValidWebSocketOrigin,
}

// isValidWebSocketOrigin validates WebSocket origins to prevent CSWSH attacks.
// Accepts localhost/127.0.0.1 connections and non-browser clients (no Origin header).
func isValidWebSocketOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		slog.Warn("websocket: invalid origin header", "origin", origin, "error", err)
		return false
	}
	hostname := u.Hostname()
	if hostname == "127.0.0.1" || hostname == "localhost" || hostname == "[::1]" {
		return true
	}
	slog.Warn("websocket: rejected non-localhost origin", "origin", origin, "hostname", hostname)
	return false
}

// HandleHealthz returns 200 OK with service identity. Used by container health checks.
func HandleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"service": "yummi-orchestrator",
	})
}

// ─── Shared helpers ───────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		slog.Error("json encode failed", "error", err)
	}
}

// writeError writes an RFC 7807-style problem-details response.
func writeError(w http.ResponseWriter, status int, title string, err error) {
	detail := ""
	if err != nil {
		detail = err.Error()
		slog.Error(title, "error", err)
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type":   "about:blank",
		"title":  title,
		"status": status,
		"detail": detail,
	})
}

func normalizeSessionID(sessionID string) string {
	if sessionID == "" {
		return "default"
	}
	return sessionID
}

func localApprovalMessage(p conductor.Proposal) string {
	if p.Command == "" {
		return "Proposal approved."
	}
	return "Approved. Suggested command: " + p.Command
}

var validSeverities = map[string]struct{}{
	"info": {}, "low": {}, "medium": {}, "high": {}, "critical": {},
}

func isValidSeverity(s string) bool {
	_, ok := validSeverities[strings.ToLower(s)]
	return ok
}

func appendAuditSafe(sm *session.Manager, sessionID, eventType, actor string, payload any) {
	if sm == nil {
		return
	}
	if err := sm.AppendAudit(sessionID, eventType, actor, payload); err != nil {
		slog.Warn("append audit failed", "session_id", sessionID, "event_type", eventType, "error", err)
	}
}

// clampLimit keeps pagination limits within safe bounds.
func clampLimit(v int) int {
	const (
		minLimit = 1
		maxLimit = 1000
	)
	if v < minLimit {
		return minLimit
	}
	if v > maxLimit {
		return maxLimit
	}
	return v
}

// trackExecutionLifecycleAudit waits for an execution to reach a terminal state
// and records the outcome in the session audit trail. It is currently unwired
// (kept intentionally pending re-attachment to the chat-approve path, which
// otherwise emits no completion audit event) — see CLAUDE.md "Known seams".
//
//nolint:unused // retained deliberately; see comment above.
func trackExecutionLifecycleAudit(executions *execution.Store, sm *session.Manager, sessionID, proposalID, executionID string) {
	if executions == nil || sm == nil {
		return
	}

	rec, err := executions.WaitForTerminal(executionID, 5*time.Second)
	if err != nil {
		appendAuditSafe(sm, sessionID, "assistant.execution.observe_failed", "assistant", map[string]any{
			"execution_id": executionID,
			"proposal_id":  proposalID,
			"error":        err.Error(),
		})
		return
	}

	eventType := "assistant.execution.completed"
	if rec.Status == execution.StatusFailed {
		eventType = "assistant.execution.failed"
	}
	if rec.Status == execution.StatusCanceled {
		eventType = "assistant.execution.canceled"
	}
	appendAuditSafe(sm, sessionID, eventType, "assistant", map[string]any{
		"execution_id": rec.ID,
		"proposal_id":  proposalID,
		"status":       rec.Status,
		"output":       rec.Output,
		"error":        rec.Error,
	})
}
