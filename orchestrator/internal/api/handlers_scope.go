package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
)

// scopeRuleRequest is one program-scope rule as submitted by a client.
type scopeRuleRequest struct {
	Pattern string `json:"pattern"`
	Kind    string `json:"kind"` // "include" | "exclude"
	Note    string `json:"note,omitempty"`
}

// HandleListScopeRules returns a session's program-scope rules.
func HandleListScopeRules(ks *knowledge.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := chi.URLParam(r, "sessionID")
		if !isValidSessionID(sessionID) {
			writeError(w, http.StatusBadRequest, "Invalid session ID format", nil)
			return
		}
		rules, err := ks.ListScopeRules(r.Context(), sessionID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error(), err)
			return
		}
		if rules == nil {
			rules = make([]knowledge.ScopeRule, 0)
		}
		writeJSON(w, http.StatusOK, rules)
	}
}

// HandleCreateScopeRules accepts either a single rule ({"pattern":...,"kind":...})
// or a bulk array ({"rules":[...]}) — a bug-bounty program's published scope is
// usually many lines pasted at once, and requiring one HTTP round-trip per line
// would make importing a real program's scope table impractical.
func HandleCreateScopeRules(ks *knowledge.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := chi.URLParam(r, "sessionID")
		if !isValidSessionID(sessionID) {
			writeError(w, http.StatusBadRequest, "Invalid session ID format", nil)
			return
		}

		var body struct {
			scopeRuleRequest
			Rules []scopeRuleRequest `json:"rules,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid request body JSON", err)
			return
		}

		reqs := body.Rules
		if strings.TrimSpace(body.Pattern) != "" {
			reqs = append(reqs, body.scopeRuleRequest)
		}
		if len(reqs) == 0 {
			writeError(w, http.StatusBadRequest, "At least one scope rule (pattern + kind) is required", nil)
			return
		}

		saved := make([]knowledge.ScopeRule, 0, len(reqs))
		for _, req := range reqs {
			rule, err := ks.SaveScopeRule(r.Context(), knowledge.ScopeRule{
				SessionID: sessionID,
				Pattern:   req.Pattern,
				Kind:      knowledge.ScopeRuleKind(strings.ToLower(strings.TrimSpace(req.Kind))),
				Note:      req.Note,
			})
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error(), err)
				return
			}
			saved = append(saved, rule)
		}
		writeJSON(w, http.StatusCreated, saved)
	}
}

// HandleDeleteScopeRule removes one program-scope rule.
func HandleDeleteScopeRule(ks *knowledge.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := chi.URLParam(r, "sessionID")
		ruleID := chi.URLParam(r, "ruleID")
		if !isValidSessionID(sessionID) {
			writeError(w, http.StatusBadRequest, "Invalid session ID format", nil)
			return
		}
		ok, err := ks.DeleteScopeRule(r.Context(), sessionID, ruleID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error(), err)
			return
		}
		if !ok {
			writeError(w, http.StatusNotFound, "Scope rule not found", nil)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// HandleClassifyScope classifies a hostname against a session's program-scope
// rules — used by the recon pipeline to auto-tag newly discovered subdomains,
// and by an operator/agent checking a single host before acting on it. This
// is knowledge-layer bookkeeping only; it never gates command execution
// (the gateway's own network scope is the sole execution-time authority).
func HandleClassifyScope(ks *knowledge.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := chi.URLParam(r, "sessionID")
		if !isValidSessionID(sessionID) {
			writeError(w, http.StatusBadRequest, "Invalid session ID format", nil)
			return
		}

		var req struct {
			Hostname  string   `json:"hostname"`
			Hostnames []string `json:"hostnames,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid request body JSON", err)
			return
		}
		hosts := req.Hostnames
		if strings.TrimSpace(req.Hostname) != "" {
			hosts = append(hosts, req.Hostname)
		}
		if len(hosts) == 0 {
			writeError(w, http.StatusBadRequest, "hostname or hostnames is required", nil)
			return
		}

		rules, err := ks.ListScopeRules(r.Context(), sessionID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error(), err)
			return
		}

		out := make(map[string]knowledge.ScopeVerdict, len(hosts))
		for _, h := range hosts {
			out[h] = knowledge.ClassifyScope(rules, h)
		}
		writeJSON(w, http.StatusOK, out)
	}
}
