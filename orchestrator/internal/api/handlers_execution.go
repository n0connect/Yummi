package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/n0connect/yummi/orchestrator/internal/execution"
	"github.com/n0connect/yummi/orchestrator/internal/executor"
	"github.com/n0connect/yummi/orchestrator/internal/gateway"
	"github.com/n0connect/yummi/orchestrator/internal/session"
)

func HandleListExecutions(store *execution.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			writeError(w, http.StatusServiceUnavailable, "execution store unavailable", nil)
			return
		}
		sessionID := chi.URLParam(r, "sessionID")
		if !isValidSessionID(sessionID) {
			writeError(w, http.StatusBadRequest, "invalid session id format", nil)
			return
		}
		limit := 100
		if raw := r.URL.Query().Get("limit"); raw != "" {
			v, err := strconv.Atoi(raw)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid limit query parameter", err)
				return
			}
			limit = clampLimit(v)
		}
		records := store.ListBySession(normalizeSessionID(sessionID), limit)
		writeJSON(w, http.StatusOK, records)
	}
}

func HandleGetExecution(store *execution.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			writeError(w, http.StatusServiceUnavailable, "execution store unavailable", nil)
			return
		}
		id := chi.URLParam(r, "id")
		if !isValidSessionID(id) {
			writeError(w, http.StatusBadRequest, "invalid execution id format", nil)
			return
		}
		record, ok := store.Get(id)
		if !ok {
			writeError(w, http.StatusNotFound, "execution not found", nil)
			return
		}
		writeJSON(w, http.StatusOK, record)
	}
}

func HandleCancelExecution(store *execution.Store, sm *session.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			writeError(w, http.StatusServiceUnavailable, "execution store unavailable", nil)
			return
		}
		id := chi.URLParam(r, "id")
		if !isValidSessionID(id) {
			writeError(w, http.StatusBadRequest, "invalid execution id format", nil)
			return
		}
		rec, err := store.Cancel(id)
		if err != nil {
			status := http.StatusInternalServerError
			switch {
			case errors.Is(err, execution.ErrExecutionNotFound):
				status = http.StatusNotFound
			case errors.Is(err, execution.ErrExecutionTerminal):
				status = http.StatusConflict
			}
			appendAuditSafe(sm, normalizeSessionID(rec.SessionID), "assistant.execution.cancel_failed", "user", map[string]any{
				"execution_id": id,
				"error":        err.Error(),
			})
			writeError(w, status, "failed to cancel execution", err)
			return
		}

		appendAuditSafe(sm, normalizeSessionID(rec.SessionID), "assistant.execution.cancel_requested", "user", map[string]any{
			"execution_id": rec.ID,
			"proposal_id":  rec.ProposalID,
			"status":       rec.Status,
		})
		writeJSON(w, http.StatusOK, rec)
	}
}

// HandleResolveEgress settles an execution held in the egress-approval state
// (gateway Ask mode). On approval it opens a temporary gateway allow rule for
// the target and the execution resumes; on denial it ends as failed. This is
// the operator-facing half of the egress "ask" round-trip — the orchestrator
// holds the command fail-closed until the operator decides here.
func HandleResolveEgress(store *execution.Store, pc *gateway.Client, sm *session.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			writeError(w, http.StatusServiceUnavailable, "execution store unavailable", nil)
			return
		}
		id := chi.URLParam(r, "id")
		if !isValidSessionID(id) {
			writeError(w, http.StatusBadRequest, "invalid execution id format", nil)
			return
		}
		var req struct {
			Approved bool `json:"approved"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body", err)
			return
		}

		// Snapshot the target before resolving (ResolveEgress clears it on the
		// approved path) so the audit entry records what was decided.
		held, _ := store.Get(id)
		target := held.EgressTarget

		var pinned []string
		if held.PinnedIPs != "" {
			_ = json.Unmarshal([]byte(held.PinnedIPs), &pinned)
		}

		unlock := func() error { return executor.UnlockEgressTarget(pc, target, held.Command, pinned...) }
		rec, err := store.ResolveEgress(id, req.Approved, unlock)
		if err != nil {
			status := http.StatusInternalServerError
			switch {
			case errors.Is(err, execution.ErrExecutionNotFound):
				status = http.StatusNotFound
			case errors.Is(err, execution.ErrNotAwaitingEgress):
				status = http.StatusConflict
			}
			appendAuditSafe(sm, normalizeSessionID(held.SessionID), "assistant.execution.egress_resolve_failed", "user", map[string]any{
				"execution_id": id,
				"target":       target,
				"approved":     req.Approved,
				"error":        err.Error(),
			})
			writeError(w, status, "failed to resolve egress approval", err)
			return
		}

		appendAuditSafe(sm, normalizeSessionID(rec.SessionID), "assistant.execution.egress_resolved", "user", map[string]any{
			"execution_id": rec.ID,
			"target":       target,
			"approved":     req.Approved,
			"status":       rec.Status,
		})
		writeJSON(w, http.StatusOK, rec)
	}
}
