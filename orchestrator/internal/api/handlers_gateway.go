package api

import (
	"encoding/json"
	"net/http"
	"net/netip"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/n0connect/yummi/orchestrator/internal/gateway"
)

// validNetwork reports whether s is a syntactically valid IP address or CIDR
// prefix. net/netip bound-checks octets and prefix length, so this rejects
// malformed input (out-of-range octets, bad masks) without a blacklist.
func validNetwork(s string) bool {
	if _, err := netip.ParseAddr(s); err == nil {
		return true
	}
	_, err := netip.ParsePrefix(s)
	return err == nil
}

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
		if len(req.Network) > 45 {
			writeError(w, http.StatusBadRequest, "network is too long (max 45 characters)", nil)
			return
		}
		if req.TTLSecs < 1 || req.TTLSecs > 86400 {
			writeError(w, http.StatusBadRequest, "ttl_secs must be between 1 and 86400", nil)
			return
		}
		if !reasonPattern.MatchString(req.Reason) {
			writeError(w, http.StatusBadRequest, "reason must be 1-500 characters and contain only alphanumeric, spaces, and basic punctuation", nil)
			return
		}
		// Syntax-validate the target as an IP or CIDR before it ever reaches the
		// gateway/nftables. The gateway is the authority and re-validates, but a
		// structural check here rejects garbage with a clean 400 (instead of a
		// 502 round-trip) and keeps malformed input out of the nftables path.
		if !validNetwork(req.Network) {
			writeError(w, http.StatusBadRequest, "network must be a valid IP or CIDR (e.g. 10.0.0.5 or 10.0.0.0/24)", nil)
			return
		}
		if c == nil {
			writeError(w, http.StatusServiceUnavailable, "gateway not configured", nil)
			return
		}
		ruleID, expiresAt, kernelWarning, err := c.AddManualRule(req.Network, req.TTLSecs, req.Reason)
		if err != nil {
			writeError(w, http.StatusBadGateway, "failed to add rule", err)
			return
		}
		resp := map[string]any{"rule_id": ruleID, "expires_at": expiresAt}
		if kernelWarning != "" {
			resp["kernel_warning"] = kernelWarning
		}
		writeJSON(w, http.StatusOK, resp)
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
		if !isValidSessionID(ruleID) {
			writeError(w, http.StatusBadRequest, "invalid rule id format", nil)
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
				limit = clampLimit(parsed)
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
