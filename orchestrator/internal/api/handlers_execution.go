package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/n0connect/yummi/orchestrator/internal/execution"
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
