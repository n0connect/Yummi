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
		if msg := threatscan.FirstThreatMessage(s.Name+"\n"+s.Description+"\n"+s.Knowledge, threatscan.ScopeStrict); msg != "" {
			writeError(w, http.StatusConflict, "draft failed re-scan", errors.New(msg))
			return
		}
		s = skill.Normalize(s)
		if err := skill.Validate(s); err != nil {
			writeError(w, http.StatusConflict, "draft failed schema validation", err)
			return
		}

		var req struct {
			Reason string `json:"reason"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		// Claim pending->activating first. "approved" is committed only after
		// durable file persistence and live-registry activation both succeed.
		if _, err := store.ClaimActivation(r.Context(), id); err != nil {
			writeError(w, http.StatusConflict, "activation claim failed", err)
			return
		}

		var activationErr error
		if skillDir != "" {
			name := skillFilenameSanitizer.ReplaceAllString(s.Name, "-")
			if name == "" {
				name = "skill-" + id
			}
			activationErr = writeSkillAtomically(skillDir, name+".yaml", []byte(ps.DraftYAML))
		}
		if activationErr == nil && reg != nil {
			activationErr = reg.Upsert(s)
		}
		if activationErr != nil {
			_, _ = store.CompleteActivation(r.Context(), id, activationErr)
			writeError(w, http.StatusInternalServerError, "skill activation failed", activationErr)
			return
		}
		if _, err := store.CompleteActivation(r.Context(), id, nil); err != nil {
			writeError(w, http.StatusInternalServerError, "activation commit failed", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"resolved": true})
	}
}

func writeSkillAtomically(dir, name string, body []byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".skill-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o644); err != nil {
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, filepath.Join(dir, name)); err != nil {
		return err
	}
	committed = true
	return nil
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
