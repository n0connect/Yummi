package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/n0connect/yummi/orchestrator/internal/agent"
)

// HandleCreateTask creates an agent task and synchronously generates its plan.
// POST /api/v1/tasks  {"session_id":"...","goal":"..."}
func HandleCreateTask(runner *agent.Runner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runner == nil {
			writeError(w, http.StatusServiceUnavailable, "agent unavailable", errors.New("agent runner not configured"))
			return
		}
		var req struct {
			SessionID string `json:"session_id"`
			Goal      string `json:"goal"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Goal) == "" {
			writeError(w, http.StatusBadRequest, "invalid request", errors.New("goal is required"))
			return
		}
		task, err := runner.CreateAndPlan(r.Context(), req.SessionID, req.Goal)
		if err != nil {
			writeError(w, http.StatusBadGateway, "planning failed", err)
			return
		}
		writeJSON(w, http.StatusCreated, task)
	}
}

// HandleGetTask returns one task. GET /api/v1/tasks/{id}
func HandleGetTask(store *agent.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		task, ok := store.Get(chi.URLParam(r, "id"))
		if !ok {
			writeError(w, http.StatusNotFound, "not found", errors.New("no such task"))
			return
		}
		writeJSON(w, http.StatusOK, task)
	}
}

// HandleListTasks returns tasks for a session.
// GET /api/v1/sessions/{sessionID}/tasks
func HandleListTasks(store *agent.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, store.ListBySession(chi.URLParam(r, "sessionID"), 50))
	}
}

// HandleStartTask starts (or resumes) the loop. POST /api/v1/tasks/{id}/start
func HandleStartTask(runner *agent.Runner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := runner.Start(chi.URLParam(r, "id")); err != nil {
			writeError(w, http.StatusConflict, "cannot start", err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "running"})
	}
}

// HandleStopTask stops the loop. POST /api/v1/tasks/{id}/stop
func HandleStopTask(runner *agent.Runner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := runner.Stop(chi.URLParam(r, "id")); err != nil {
			writeError(w, http.StatusNotFound, "cannot stop", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
	}
}

// HandleApproveTask resolves the pending risk-gate verdict.
// POST /api/v1/tasks/{id}/approve {"proposal_id":"...","approved":true,"reason":""}
func HandleApproveTask(runner *agent.Runner, store *agent.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		taskID := chi.URLParam(r, "id")
		var req struct {
			ProposalID string `json:"proposal_id"`
			Approved   bool   `json:"approved"`
			Reason     string `json:"reason"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ProposalID == "" {
			writeError(w, http.StatusBadRequest, "invalid request", errors.New("proposal_id is required"))
			return
		}
		if err := runner.Approve(taskID, req.ProposalID, req.Approved, req.Reason); err != nil {
			writeError(w, http.StatusNotFound, "no pending approval", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"resolved": true})
	}
}
