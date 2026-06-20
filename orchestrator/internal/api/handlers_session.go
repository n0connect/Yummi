package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/n0connect/yummi/orchestrator/internal/session"
)

// --- Session Handlers ---

func HandleListSessions(sm *session.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessions, err := sm.List()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list sessions", err)
			return
		}
		writeJSON(w, http.StatusOK, sessions)
	}
}

func HandleCreateSession(sm *session.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body", err)
			return
		}
		if req.Name == "" {
			writeError(w, http.StatusBadRequest, "name is required", nil)
			return
		}

		s := session.Session{
			ID:           uuid.New().String(),
			Name:         req.Name,
			CreatedAt:    time.Now().Unix(),
			UpdatedAt:    time.Now().Unix(),
			Status:       "active",
			GatewayLevel: 0,
		}

		if err := sm.Create(&s); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create session", err)
			return
		}
		writeJSON(w, http.StatusCreated, s)
	}
}

func HandleGetSession(sm *session.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		s, err := sm.Get(id)
		if err != nil {
			writeError(w, http.StatusNotFound, "session not found", err)
			return
		}
		writeJSON(w, http.StatusOK, s)
	}
}

func HandleUpdateSession(sm *session.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var req struct {
			Name         *string `json:"name,omitempty"`
			Status       *string `json:"status,omitempty"`
			GatewayLevel *int    `json:"gateway_level,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body", err)
			return
		}

		s, err := sm.Get(id)
		if err != nil {
			writeError(w, http.StatusNotFound, "session not found", err)
			return
		}

		if req.Name != nil {
			s.Name = *req.Name
		}
		if req.Status != nil {
			s.Status = *req.Status
		}
		if req.GatewayLevel != nil {
			s.GatewayLevel = *req.GatewayLevel
		}
		s.UpdatedAt = time.Now().Unix()

		if err := sm.Update(s); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update session", err)
			return
		}
		writeJSON(w, http.StatusOK, s)
	}
}

func HandleDeleteSession(sm *session.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := sm.Delete(id); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to delete session", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// --- Target Handlers ---

func HandleListTargets(sm *session.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := chi.URLParam(r, "sessionID")
		targets, err := sm.ListTargets(sessionID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list targets", err)
			return
		}
		writeJSON(w, http.StatusOK, targets)
	}
}

func HandleCreateTarget(sm *session.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := chi.URLParam(r, "sessionID")
		var req struct {
			Value   string `json:"value"`
			InScope bool   `json:"in_scope"`
			Notes   string `json:"notes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body", err)
			return
		}
		if req.Value == "" {
			writeError(w, http.StatusBadRequest, "value is required", nil)
			return
		}

		t := session.Target{
			ID:        uuid.New().String(),
			SessionID: sessionID,
			Value:     req.Value,
			InScope:   req.InScope,
			Notes:     req.Notes,
			CreatedAt: time.Now().Unix(),
		}

		if err := sm.CreateTarget(&t); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create target", err)
			return
		}
		writeJSON(w, http.StatusCreated, t)
	}
}

// --- Finding Handlers ---

func HandleListFindings(sm *session.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := chi.URLParam(r, "sessionID")
		findings, err := sm.ListFindings(sessionID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list findings", err)
			return
		}
		writeJSON(w, http.StatusOK, findings)
	}
}

func HandleCreateFinding(sm *session.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := chi.URLParam(r, "sessionID")
		var req struct {
			Severity    string `json:"severity"`
			Title       string `json:"title"`
			Description string `json:"description"`
			Evidence    string `json:"evidence"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body", err)
			return
		}
		if req.Title == "" || req.Severity == "" {
			writeError(w, http.StatusBadRequest, "title and severity are required", nil)
			return
		}
		if !isValidSeverity(req.Severity) {
			writeError(w, http.StatusBadRequest, "severity must be one of: info, low, medium, high, critical", nil)
			return
		}

		f := session.Finding{
			ID:          uuid.New().String(),
			SessionID:   sessionID,
			Severity:    req.Severity,
			Title:       req.Title,
			Description: req.Description,
			Evidence:    req.Evidence,
			CreatedAt:   time.Now().Unix(),
		}

		if err := sm.CreateFinding(&f); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create finding", err)
			return
		}
		writeJSON(w, http.StatusCreated, f)
	}
}

func HandleListAudit(sm *session.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := chi.URLParam(r, "sessionID")
		limit := 100
		if raw := r.URL.Query().Get("limit"); raw != "" {
			v, err := strconv.Atoi(raw)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid limit query parameter", err)
				return
			}
			limit = clampLimit(v)
		}

		events, err := sm.ListAudit(sessionID, limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list audit events", err)
			return
		}
		writeJSON(w, http.StatusOK, events)
	}
}
