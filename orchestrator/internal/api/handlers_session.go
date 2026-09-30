package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
	"github.com/n0connect/yummi/orchestrator/internal/scope"
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
		if !isValidName(req.Name) {
			writeError(w, http.StatusBadRequest, "name must be 1-100 characters and only contain alphanumeric, spaces, underscores, or dashes", nil)
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
		if !isValidSessionID(id) {
			writeError(w, http.StatusBadRequest, "invalid session id format", nil)
			return
		}
		s, err := sm.Get(id)
		if err != nil {
			writeError(w, http.StatusNotFound, "session not found", err)
			return
		}
		writeJSON(w, http.StatusOK, s)
	}
}

// HandleListMessages returns a session's persisted conversation in
// chronological order, so the UI can rehydrate a chat on reload or when the
// operator switches sessions in the sidebar.
func HandleListMessages(sm *session.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if !isValidSessionID(id) {
			writeError(w, http.StatusBadRequest, "invalid session id format", nil)
			return
		}
		limit := 200
		if raw := r.URL.Query().Get("limit"); raw != "" {
			if v, err := strconv.Atoi(raw); err == nil {
				limit = clampLimit(v)
			}
		}
		msgs, err := sm.ListMessages(normalizeSessionID(id), limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list messages", err)
			return
		}
		writeJSON(w, http.StatusOK, msgs)
	}
}

func HandleUpdateSession(sm *session.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if !isValidSessionID(id) {
			writeError(w, http.StatusBadRequest, "invalid session id format", nil)
			return
		}
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
			if !isValidName(*req.Name) {
				writeError(w, http.StatusBadRequest, "name must be 1-100 characters and only contain alphanumeric, spaces, underscores, or dashes", nil)
				return
			}
			s.Name = *req.Name
		}
		if req.Status != nil {
			status := *req.Status
			if status != "active" && status != "closed" {
				writeError(w, http.StatusBadRequest, "status must be either active or closed", nil)
				return
			}
			s.Status = status
		}
		if req.GatewayLevel != nil {
			lvl := *req.GatewayLevel
			if lvl < 0 || lvl > 5 {
				writeError(w, http.StatusBadRequest, "gateway_level must be between 0 and 5", nil)
				return
			}
			s.GatewayLevel = lvl
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
		if !isValidSessionID(id) {
			writeError(w, http.StatusBadRequest, "invalid session id format", nil)
			return
		}
		if err := sm.Delete(id); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to delete session", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// --- Target Handlers ---

func HandleListTargets(ks *knowledge.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := chi.URLParam(r, "sessionID")
		if !isValidSessionID(sessionID) {
			writeError(w, http.StatusBadRequest, "invalid session id format", nil)
			return
		}
		writeJSON(w, http.StatusOK, ks.ListTargets(sessionID))
	}
}

func HandleCreateTarget(ks *knowledge.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := chi.URLParam(r, "sessionID")
		if !isValidSessionID(sessionID) {
			writeError(w, http.StatusBadRequest, "invalid session id format", nil)
			return
		}
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
		targetVal := scope.ExtractTarget("dummy " + req.Value)
		if targetVal == "" || len(req.Value) > 253 {
			writeError(w, http.StatusBadRequest, "value must be a valid IP, CIDR, hostname, or URL (max 253 chars)", nil)
			return
		}
		if len(req.Notes) > 1000 {
			writeError(w, http.StatusBadRequest, "notes cannot exceed 1000 characters", nil)
			return
		}

		t := knowledge.Target{
			ID:        uuid.New().String(),
			SessionID: sessionID,
			Value:     targetVal,
			InScope:   req.InScope,
			Notes:     req.Notes,
			CreatedAt: time.Now().Unix(),
		}

		if err := ks.CreateTarget(r.Context(), t); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create target", err)
			return
		}
		writeJSON(w, http.StatusCreated, t)
	}
}

// --- Finding Handlers ---

func HandleListFindings(ks *knowledge.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := chi.URLParam(r, "sessionID")
		if !isValidSessionID(sessionID) {
			writeError(w, http.StatusBadRequest, "invalid session id format", nil)
			return
		}
		writeJSON(w, http.StatusOK, ks.ListFindings(sessionID))
	}
}

func HandleCreateFinding(ks *knowledge.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := chi.URLParam(r, "sessionID")
		if !isValidSessionID(sessionID) {
			writeError(w, http.StatusBadRequest, "invalid session id format", nil)
			return
		}
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
		if !titlePattern.MatchString(req.Title) {
			writeError(w, http.StatusBadRequest, "title must be 1-200 characters and contain only alphanumeric, spaces, and basic punctuation", nil)
			return
		}
		if len(req.Description) > 5000 {
			writeError(w, http.StatusBadRequest, "description cannot exceed 5000 characters", nil)
			return
		}
		if len(req.Evidence) > 10000 {
			writeError(w, http.StatusBadRequest, "evidence cannot exceed 10000 characters", nil)
			return
		}

		f := knowledge.Finding{
			ID:          uuid.New().String(),
			SessionID:   sessionID,
			Severity:    req.Severity,
			Title:       req.Title,
			Description: req.Description,
			Evidence:    req.Evidence,
			Source:      "manual",
			CreatedAt:   time.Now().Unix(),
		}

		if err := ks.RecordFinding(r.Context(), f); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create finding", err)
			return
		}
		writeJSON(w, http.StatusCreated, f)
	}
}

func HandleListAudit(sm *session.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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

		events, err := sm.ListAudit(sessionID, limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list audit events", err)
			return
		}
		writeJSON(w, http.StatusOK, events)
	}
}
