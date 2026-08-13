package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/n0connect/yummi/orchestrator/internal/agent"
	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
	"github.com/n0connect/yummi/orchestrator/internal/skill"
)

// qLimit reads a positive ?limit= query parameter, falling back to def. The
// value is clamped to the shared safe bound so a caller cannot request an
// unboundedly large result set.
func qLimit(r *http.Request, def int) int {
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		return clampLimit(v)
	}
	return def
}

// HandleDeleteContext explicitly forgets one persistent memory entry.
// DELETE /api/v1/sessions/{sessionID}/context/{key}
func HandleDeleteContext(ks *knowledge.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ks == nil {
			writeError(w, http.StatusServiceUnavailable, "knowledge store unavailable", nil)
			return
		}
		sessionID := chi.URLParam(r, "sessionID")
		key := strings.TrimSpace(chi.URLParam(r, "key"))
		if !isValidSessionID(sessionID) || key == "" || len([]rune(key)) > 200 {
			writeError(w, http.StatusBadRequest, "invalid session id or context key", nil)
			return
		}
		if err := ks.DeleteContext(r.Context(), sessionID, key); err != nil {
			writeError(w, http.StatusNotFound, "context not found", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// HandleListActivity returns a session's legible activity trace.
// GET /api/v1/sessions/{sessionID}/activity?limit=N
func HandleListActivity(store *agent.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			writeJSON(w, http.StatusOK, []agent.ActivityEvent{})
			return
		}
		sessionID := chi.URLParam(r, "sessionID")
		if !isValidSessionID(sessionID) {
			writeError(w, http.StatusBadRequest, "invalid session id format", nil)
			return
		}
		writeJSON(w, http.StatusOK, store.ListActivity(sessionID, qLimit(r, 200)))
	}
}

// HandleListTactics returns learned procedural tactics — by tool or across all.
// GET /api/v1/tactics?tool=X&limit=N
func HandleListTactics(ks *knowledge.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ks == nil {
			writeJSON(w, http.StatusOK, []knowledge.Tactic{})
			return
		}
		if tool := r.URL.Query().Get("tool"); tool != "" {
			writeJSON(w, http.StatusOK, ks.ListTactics(tool, qLimit(r, 50)))
			return
		}
		writeJSON(w, http.StatusOK, ks.ListAllTactics(qLimit(r, 50)))
	}
}

// HandleListContext returns a session's persistent memory (code maps, etc).
// GET /api/v1/sessions/{sessionID}/context
func HandleListContext(ks *knowledge.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ks == nil {
			writeJSON(w, http.StatusOK, []knowledge.PersistentContext{})
			return
		}
		sessionID := chi.URLParam(r, "sessionID")
		if !isValidSessionID(sessionID) {
			writeError(w, http.StatusBadRequest, "invalid session id format", nil)
			return
		}
		writeJSON(w, http.StatusOK, ks.ListContexts(sessionID))
	}
}

// HandleListSkills returns the loaded skill playbooks. GET /api/v1/skills
func HandleListSkills(reg *skill.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, reg.All())
	}
}

// HandleListRoles returns the available sub-agent roles. GET /api/v1/agent/roles
func HandleListRoles(runner agent.TaskRunner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runner == nil {
			writeJSON(w, http.StatusOK, []string{})
			return
		}
		writeJSON(w, http.StatusOK, runner.Roles())
	}
}
