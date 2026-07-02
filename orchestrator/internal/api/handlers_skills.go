package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"

	"github.com/n0connect/yummi/orchestrator/internal/security/threatscan"
	"github.com/n0connect/yummi/orchestrator/internal/skill"
	"github.com/n0connect/yummi/orchestrator/internal/skillcurator"
)

// HandleListPendingSkills lists curator-drafted skills. GET
// /api/v1/skills/pending?status=pending (default "pending").
func HandleListPendingSkills(store *skillcurator.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			writeError(w, http.StatusServiceUnavailable, "skill curator unavailable", errors.New("skillcurator store not configured"))
			return
		}
		status := r.URL.Query().Get("status")
		if status == "" {
			status = "pending"
		}
		items, err := store.List(r.Context(), status)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "list failed", err)
			return
		}
		writeJSON(w, http.StatusOK, items)
	}
}

var skillFilenameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

// HandleApprovePendingSkill promotes a pending draft to a live, loaded skill.
// POST /api/v1/skills/pending/{id}/approve {"reason":""}
//
// Re-scans the draft with threatscan before writing anything — defense in
// depth, independent of Curator.ProposeFromTask's own scan, in case a row
// ever reaches "pending" status through a path that didn't already scan it.
func HandleApprovePendingSkill(store *skillcurator.Store, reg *skill.Registry, skillDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			writeError(w, http.StatusServiceUnavailable, "skill curator unavailable", errors.New("skillcurator store not configured"))
			return
		}
		id := chi.URLParam(r, "id")
		ps, ok, err := store.Get(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "lookup failed", err)
			return
		}
		if !ok {
			writeError(w, http.StatusNotFound, "not found", errors.New("no such pending skill"))
			return
		}

		var s skill.Skill
		if err := yaml.Unmarshal([]byte(ps.DraftYAML), &s); err != nil {
			writeError(w, http.StatusInternalServerError, "malformed draft", err)
			return
		}
		if msg := threatscan.FirstThreatMessage(s.Description+"\n"+s.Knowledge, threatscan.ScopeStrict); msg != "" {
			writeError(w, http.StatusConflict, "draft failed re-scan", errors.New(msg))
			return
		}

		if skillDir != "" {
			if err := os.MkdirAll(skillDir, 0o755); err != nil {
				writeError(w, http.StatusInternalServerError, "write failed", err)
				return
			}
			name := skillFilenameSanitizer.ReplaceAllString(s.Name, "-")
			if name == "" {
				name = "skill-" + id
			}
			path := filepath.Join(skillDir, name+".yaml")
			if err := os.WriteFile(path, []byte(ps.DraftYAML), 0o644); err != nil {
				writeError(w, http.StatusInternalServerError, "write failed", err)
				return
			}
		}
		if reg != nil {
			reg.Add(s)
		}

		var req struct {
			Reason string `json:"reason"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if _, err := store.Resolve(r.Context(), id, true, req.Reason); err != nil {
			writeError(w, http.StatusConflict, "resolve failed", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"resolved": true})
	}
}

// HandleRejectPendingSkill discards a pending draft. POST
// /api/v1/skills/pending/{id}/reject {"reason":""}
func HandleRejectPendingSkill(store *skillcurator.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			writeError(w, http.StatusServiceUnavailable, "skill curator unavailable", errors.New("skillcurator store not configured"))
			return
		}
		id := chi.URLParam(r, "id")
		var req struct {
			Reason string `json:"reason"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if _, err := store.Resolve(r.Context(), id, false, req.Reason); err != nil {
			writeError(w, http.StatusNotFound, "resolve failed", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"resolved": true})
	}
}
