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
func HandleCreateTask(runner agent.TaskRunner) http.HandlerFunc {
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
		if !checkOptionalSessionID(w, req.SessionID) {
			return
		}
		if len(req.Goal) > 10000 {
			writeError(w, http.StatusBadRequest, "goal cannot exceed 10000 characters", nil)
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
		id := chi.URLParam(r, "id")
		if !isValidSessionID(id) {
			writeError(w, http.StatusBadRequest, "invalid task id format", nil)
			return
		}
		task, ok := store.Get(id)
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
		sessionID := chi.URLParam(r, "sessionID")
		if !isValidSessionID(sessionID) {
			writeError(w, http.StatusBadRequest, "invalid session id format", nil)
			return
		}
		writeJSON(w, http.StatusOK, store.ListBySession(sessionID, 50))
	}
}

// HandleStartTask starts (or resumes) the loop. POST /api/v1/tasks/{id}/start
// Body is optional: {"message":"..."} carries the operator's answer when
// resuming a task paused via [ASK_OPERATOR] — without it, the model would
// resume with nothing to go on. A missing/empty/malformed body is fine (plain
// resume, or the initial Start of a freshly planned task).
func HandleStartTask(runner agent.TaskRunner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if !isValidSessionID(id) {
			writeError(w, http.StatusBadRequest, "invalid task id format", nil)
			return
		}
		var req struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if len(req.Message) > 10000 {
			writeError(w, http.StatusBadRequest, "message cannot exceed 10000 characters", nil)
			return
		}
		if err := runner.Start(id, req.Message); err != nil {
			writeError(w, http.StatusConflict, "cannot start", err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "running"})
	}
}

// HandleStopTask stops the loop. POST /api/v1/tasks/{id}/stop
func HandleStopTask(runner agent.TaskRunner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if !isValidSessionID(id) {
			writeError(w, http.StatusBadRequest, "invalid task id format", nil)
			return
		}
		if err := runner.Stop(id); err != nil {
			writeError(w, http.StatusNotFound, "cannot stop", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
	}
}

// HandleListDelegations returns active and recent specialist runs.
// GET /api/v1/agent/delegations
func HandleListDelegations(runner agent.TaskRunner) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if runner == nil {
			writeJSON(w, http.StatusOK, []agent.DelegationRun{})
			return
		}
		writeJSON(w, http.StatusOK, runner.DelegationRuns())
	}
}

// HandleAgentCapabilities exposes the selected backend's real operational
// contract (recovery, tool loop, delegation, memory and skill governance).
func HandleAgentCapabilities(runner agent.TaskRunner) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if runner == nil {
			writeError(w, http.StatusServiceUnavailable, "agent unavailable", errors.New("agent runner not configured"))
			return
		}
		writeJSON(w, http.StatusOK, runner.Capabilities())
	}
}

// HandleInterruptDelegation cancels one specialist while leaving its parent
// and siblings running. POST /api/v1/agent/delegations/{id}/interrupt
func HandleInterruptDelegation(runner agent.TaskRunner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runner == nil {
			writeError(w, http.StatusServiceUnavailable, "agent unavailable", errors.New("agent runner not configured"))
			return
		}
		id := chi.URLParam(r, "id")
		if !isValidSessionID(id) {
			writeError(w, http.StatusBadRequest, "invalid delegation id format", nil)
			return
		}
		if err := runner.InterruptDelegation(id); err != nil {
			writeError(w, http.StatusConflict, "cannot interrupt delegation", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "interrupted"})
	}
}

// HandleGetApprovalMode returns the agent's current approval mode.
// GET /api/v1/agent/mode
func HandleGetApprovalMode(runner agent.TaskRunner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runner == nil {
			writeError(w, http.StatusServiceUnavailable, "agent unavailable", errors.New("agent runner not configured"))
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"mode": string(runner.ApprovalMode())})
	}
}

// HandleSetApprovalMode switches the agent between ask_permission and auto.
// POST /api/v1/agent/mode  {"mode":"auto"|"ask_permission"}
func HandleSetApprovalMode(runner agent.TaskRunner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runner == nil {
			writeError(w, http.StatusServiceUnavailable, "agent unavailable", errors.New("agent runner not configured"))
			return
		}
		var req struct {
			Mode string `json:"mode"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request", err)
			return
		}
		if req.Mode != "auto" && req.Mode != "ask_permission" {
			writeError(w, http.StatusBadRequest, "invalid mode: must be auto or ask_permission", nil)
			return
		}
		if err := runner.SetApprovalMode(agent.ApprovalMode(req.Mode)); err != nil {
			writeError(w, http.StatusBadRequest, "invalid mode", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"mode": req.Mode})
	}
}

// HandleApproveTask resolves the pending risk-gate verdict.
// POST /api/v1/tasks/{id}/approve {"proposal_id":"...","approved":true,"reason":""}
func HandleApproveTask(runner agent.TaskRunner, store *agent.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		taskID := chi.URLParam(r, "id")
		if !isValidSessionID(taskID) {
			writeError(w, http.StatusBadRequest, "invalid task id format", nil)
			return
		}
		var req struct {
			ProposalID string `json:"proposal_id"`
			Approved   bool   `json:"approved"`
			Reason     string `json:"reason"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ProposalID == "" {
			writeError(w, http.StatusBadRequest, "invalid request", errors.New("proposal_id is required"))
			return
		}
		if !isValidSessionID(req.ProposalID) {
			writeError(w, http.StatusBadRequest, "invalid proposal id format", nil)
			return
		}
		if len(req.Reason) > 500 {
			writeError(w, http.StatusBadRequest, "reason cannot exceed 500 characters", nil)
			return
		}
		if err := runner.Approve(taskID, req.ProposalID, req.Approved, req.Reason); err != nil {
			writeError(w, http.StatusNotFound, "no pending approval", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"resolved": true})
	}
}

// HandleSetTaskAutoApprove flips one running task's own AutoApproveNonCritical
// override (see Task's own doc comment in internal/agent/task.go) — distinct
// from the runner-wide POST /api/v1/agent/mode, which only ever applies to
// tasks started after the change, never one already mid-loop. This is what
// lets an operator watching a long ask_permission-mode task stop clicking
// approve for the REST of just this one task, without silently trusting
// every future task or losing the hard gate on high/critical risk.
func HandleSetTaskAutoApprove(runner agent.TaskRunner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		taskID := chi.URLParam(r, "id")
		if !isValidSessionID(taskID) {
			writeError(w, http.StatusBadRequest, "invalid task id format", nil)
			return
		}
		var req struct {
			Enable bool `json:"enable"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request", err)
			return
		}
		if err := runner.SetTaskAutoApprove(taskID, req.Enable); err != nil {
			writeError(w, http.StatusNotFound, "task not found", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"auto_approve_non_critical": req.Enable})
	}
}
