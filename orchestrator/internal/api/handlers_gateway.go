package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/n0connect/yummi/orchestrator/internal/gateway"
)

// HandleGatewayRules returns all currently active gateway policy rules.
func HandleGatewayRules(c *gateway.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if c == nil {
			writeJSON(w, http.StatusOK, map[string]any{"rules": []gateway.RuleView{}, "status": "not_configured"})
			return
		}
		rules, err := c.ListRules()
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"rules": []gateway.RuleView{}, "status": "unreachable", "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"rules": rules, "status": "connected"})
	}
}

type addGatewayRuleRequest struct {
	Network string `json:"network"`
	TTLSecs uint64 `json:"ttl_secs"`
	Reason  string `json:"reason"`
}

// HandleGatewayAddRule adds an operator-issued temporary rule. The reason
// field is mandatory — this is the orchestrator-side half of the defense in
// depth; the gateway itself also rejects an empty reason.
func HandleGatewayAddRule(c *gateway.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req addGatewayRuleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body", err)
			return
		}
		if req.Reason == "" {
			writeError(w, http.StatusBadRequest, "reason is required", nil)
			return
		}
		if req.Network == "" {
			writeError(w, http.StatusBadRequest, "network is required", nil)
			return
		}
		if c == nil {
			writeError(w, http.StatusServiceUnavailable, "gateway not configured", nil)
			return
		}
		ruleID, expiresAt, err := c.AddManualRule(req.Network, req.TTLSecs, req.Reason)
		if err != nil {
			writeError(w, http.StatusBadGateway, "failed to add rule", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"rule_id": ruleID, "expires_at": expiresAt})
	}
}

// HandleGatewayRemoveRule removes a rule by ID.
func HandleGatewayRemoveRule(c *gateway.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ruleID := chi.URLParam(r, "id")
		if ruleID == "" {
			writeError(w, http.StatusBadRequest, "rule id is required", nil)
			return
		}
		if c == nil {
			writeError(w, http.StatusServiceUnavailable, "gateway not configured", nil)
			return
		}
		if err := c.RemoveRule(ruleID); err != nil {
			writeError(w, http.StatusBadGateway, "failed to remove rule", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"removed": ruleID})
	}
}

// HandleGatewayFingerprintConfig returns the gateway's fingerprint-masking configuration.
func HandleGatewayFingerprintConfig(c *gateway.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if c == nil {
			writeJSON(w, http.StatusOK, map[string]any{"status": "not_configured"})
			return
		}
		cfg, err := c.GetFingerprintConfig()
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"status": "unreachable", "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ttl_normalize":     cfg.TTLNormalize,
			"mac_randomization": cfg.MACRandomization,
			"status":            "connected",
		})
	}
}

// HandleGatewayEgressMode returns the gateway's current egress mode.
func HandleGatewayEgressMode(c *gateway.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if c == nil {
			writeJSON(w, http.StatusOK, map[string]any{"status": "not_configured"})
			return
		}
		mode, err := c.GetEgressMode()
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"status": "unreachable", "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"mode": mode, "status": "connected"})
	}
}

type setEgressModeRequest struct {
	Mode string `json:"mode"`
}

// HandleGatewaySetEgressMode sets the gateway's egress mode. The body is
// {"mode":"block"|"allow"|"ask"}; an invalid mode is rejected with 400 before
// reaching the gateway.
func HandleGatewaySetEgressMode(c *gateway.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req setEgressModeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body", err)
			return
		}
		switch req.Mode {
		case "block", "allow", "ask":
		default:
			writeError(w, http.StatusBadRequest, "mode must be one of block, allow, ask", nil)
			return
		}
		if c == nil {
			writeError(w, http.StatusServiceUnavailable, "gateway not configured", nil)
			return
		}
		mode, err := c.SetEgressMode(req.Mode)
		if err != nil {
			writeError(w, http.StatusBadGateway, "failed to set egress mode", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"mode": mode})
	}
}

// HandleGatewayAudit returns the most recent N audit log rows plus the
// chain-verification result.
func HandleGatewayAudit(c *gateway.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := 50
		if v := r.URL.Query().Get("limit"); v != "" {
			if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
				limit = parsed
			}
		}
		if c == nil {
			writeJSON(w, http.StatusOK, map[string]any{"rows": []gateway.AuditRowView{}, "chain_verified": false, "status": "not_configured"})
			return
		}
		rows, verified, err := c.GetAuditLog(limit)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"rows": []gateway.AuditRowView{}, "chain_verified": false, "status": "unreachable", "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"rows": rows, "chain_verified": verified, "status": "connected"})
	}
}
