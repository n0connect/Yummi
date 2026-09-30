package api

import (
	"encoding/json"
	"net/http"
	"net/netip"
	"regexp"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/n0connect/yummi/orchestrator/internal/gateway"
)

// hostnamePatternPattern allows a hostname glob: optional leading "*.", then
// dot-separated alphanumeric/hyphen labels. Rejects whitespace, slashes,
// schemes, and other junk before it reaches the gateway.
var hostnamePatternPattern = regexp.MustCompile(`^(\*\.)?([a-zA-Z0-9-]+\.)*[a-zA-Z0-9-]+$`)

type hostnameRuleRequest struct {
	List    string `json:"list"`    // "allow" | "block"
	Pattern string `json:"pattern"` // e.g. "*.example.com"
}

func decodeHostnameRule(w http.ResponseWriter, r *http.Request) (hostnameRuleRequest, bool) {
	var req hostnameRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return req, false
	}
	if req.List != "allow" && req.List != "block" {
		writeError(w, http.StatusBadRequest, "list must be 'allow' or 'block'", nil)
		return req, false
	}
	if len(req.Pattern) < 1 || len(req.Pattern) > 253 || !hostnamePatternPattern.MatchString(req.Pattern) {
		writeError(w, http.StatusBadRequest, "pattern must be a hostname or wildcard (e.g. example.com or *.example.com)", nil)
		return req, false
	}
	return req, true
}

// HandleGatewayAddHostnameRule adds a hostname pattern to the allow/block list
// at runtime (in-memory; resets on restart). Body: {list, pattern}.
func HandleGatewayAddHostnameRule(c *gateway.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req, ok := decodeHostnameRule(w, r)
		if !ok {
			return
		}
		if c == nil {
			writeError(w, http.StatusServiceUnavailable, "gateway not configured", nil)
			return
		}
		hp, err := c.AddHostnameRule(req.List, req.Pattern)
		if err != nil {
			writeError(w, http.StatusBadGateway, "failed to add hostname rule", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"allow": hp.Allow, "block": hp.Block, "status": "connected"})
	}
}

// HandleGatewayRemoveHostnameRule removes a hostname pattern. Body: {list, pattern}.
func HandleGatewayRemoveHostnameRule(c *gateway.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req, ok := decodeHostnameRule(w, r)
		if !ok {
			return
		}
		if c == nil {
			writeError(w, http.StatusServiceUnavailable, "gateway not configured", nil)
			return
		}
		hp, err := c.RemoveHostnameRule(req.List, req.Pattern)
		if err != nil {
			writeError(w, http.StatusBadGateway, "failed to remove hostname rule", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"allow": hp.Allow, "block": hp.Block, "status": "connected"})
	}
}

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
		kernelWarning, err := c.RemoveRule(ruleID)
		if err != nil {
			writeError(w, http.StatusBadGateway, "failed to remove rule", err)
			return
		}
		resp := map[string]any{"removed": ruleID}
		if kernelWarning != "" {
			resp["kernel_warning"] = kernelWarning
		}
		writeJSON(w, http.StatusOK, resp)
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

// HandleGatewayHostnamePolicy returns the gateway's configured hostname
// allow/block patterns (operator visibility; the lists are config-level).
func HandleGatewayHostnamePolicy(c *gateway.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if c == nil {
			writeJSON(w, http.StatusOK, map[string]any{"status": "not_configured"})
			return
		}
		hp, err := c.GetHostnamePolicy()
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"status": "unreachable", "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"allow":  hp.Allow,
			"block":  hp.Block,
			"status": "connected",
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
