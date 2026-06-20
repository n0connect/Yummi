package conductor

import (
	"fmt"
	"net/netip"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/n0connect/yummi/orchestrator/internal/scope"
)

// jsonArtifactPattern catches JSON key-value noise that qwen/llama models
// sometimes embed inside the command string instead of as separate JSON fields.
// Example: "nmap 127.0.0.1','rationale':'...','risk_level':'low'"
var jsonArtifactPattern = regexp.MustCompile(`['"]\s*,\s*['"]?(rationale|risk_level|requires_approval|target_tool|id|confidence)['"?\s]*:.*$`)

// sanitizeCommand removes JSON metadata that a model may accidentally embed
// inside the command string, and trims surrounding quotes/whitespace.
func sanitizeCommand(cmd string) string {
	cmd = jsonArtifactPattern.ReplaceAllString(cmd, "")
	cmd = strings.Trim(cmd, `"' `)
	return strings.TrimSpace(cmd)
}

// ValidationConfig defines scope constraints for command proposals.
type ValidationConfig struct {
	Allow []string
	Block []string
}

// Validator enforces tool/scope/risk checks for assistant proposals.
type Validator struct {
	allowPrefixes []netip.Prefix
	blockPrefixes []netip.Prefix
	allowHosts    map[string]struct{}
	blockHosts    map[string]struct{}
}

// NewValidator creates a validator using explicit configuration.
func NewValidator(cfg ValidationConfig) *Validator {
	v := &Validator{
		allowHosts: make(map[string]struct{}),
		blockHosts: make(map[string]struct{}),
	}
	v.allowPrefixes, v.allowHosts = parseScopeRules(cfg.Allow)
	v.blockPrefixes, v.blockHosts = parseScopeRules(cfg.Block)
	return v
}

// NewValidatorFromEnv builds a validator from environment variables.
func NewValidatorFromEnv() *Validator {
	allow := splitScopeEnv(envFirst("PENTEST_SCOPE_ALLOW", "YUMMI_SCOPE_ALLOW"))
	block := splitScopeEnv(envFirst("PENTEST_SCOPE_BLOCK", "YUMMI_SCOPE_BLOCK"))
	return NewValidator(ValidationConfig{Allow: allow, Block: block})
}

// ValidateChatResponse normalizes and filters unsafe proposals.
func (v *Validator) ValidateChatResponse(resp ChatResponse) ChatResponse {
	if v == nil || len(resp.Proposals) == 0 {
		return resp
	}

	validated := make([]Proposal, 0, len(resp.Proposals))
	warnings := append([]string{}, resp.Warnings...)

	for _, p := range resp.Proposals {
		cmd := sanitizeCommand(p.Command)
		if cmd == "" {
			warnings = append(warnings, "empty proposal command was dropped")
			continue
		}
		p.Command = cmd

		tool := firstToken(cmd)

		target := proposalTarget(p)
		if target != "" && !v.InScope(target) {
			warnings = append(warnings, fmt.Sprintf("target out of scope dropped: %s", target))
			continue
		}

		risk := classifyRisk(cmd)
		p.RiskLevel = maxRisk(p.RiskLevel, risk)
		p.RequiresApproval = requiresApproval(p.RiskLevel)

		if isHighSeverity(p.RiskLevel) {
			warnings = append(warnings, fmt.Sprintf("%s-risk proposal requires explicit approval: %s", p.RiskLevel, tool))
		}
		validated = append(validated, p)
	}

	resp.Proposals = validated
	resp.Warnings = uniqStrings(warnings)

	if len(validated) == 0 {
		resp.Intent = "ask_clarification"
		if strings.TrimSpace(resp.Message) == "" {
			resp.Message = "No safe proposal could be prepared. Please share an in-scope target and desired action."
		}
		if resp.Confidence > 0.5 {
			resp.Confidence = 0.5
		}
	}

	return resp
}

// ScopeSummary renders the configured allow-scope as indented lines for
// inclusion in prompts. Empty when no scope is configured or v is nil.
func (v *Validator) ScopeSummary() string {
	if v == nil {
		return ""
	}
	var b strings.Builder
	for _, p := range v.allowPrefixes {
		b.WriteString("  - " + p.String() + "\n")
	}
	for h := range v.allowHosts {
		b.WriteString("  - " + h + "\n")
	}
	return b.String()
}

// InScope checks whether a target is allowed by configured scope rules.
func (v *Validator) InScope(target string) bool {
	target = strings.TrimSpace(strings.ToLower(target))
	if target == "" {
		return false
	}

	if ip, err := netip.ParseAddr(target); err == nil {
		if isAlwaysBlockedIP(ip) {
			return false
		}
		for _, p := range v.blockPrefixes {
			if p.Contains(ip) {
				return false
			}
		}
		if len(v.allowPrefixes) == 0 {
			return true
		}
		for _, p := range v.allowPrefixes {
			if p.Contains(ip) {
				return true
			}
		}
		return false
	}

	if _, blocked := v.blockHosts[target]; blocked {
		return false
	}
	if len(v.allowHosts) == 0 {
		return true
	}
	_, allowed := v.allowHosts[target]
	return allowed
}

func parseScopeRules(values []string) ([]netip.Prefix, map[string]struct{}) {
	prefixes := make([]netip.Prefix, 0, len(values))
	hosts := make(map[string]struct{})
	for _, raw := range values {
		rule := strings.TrimSpace(strings.ToLower(raw))
		if rule == "" {
			continue
		}
		if strings.Contains(rule, "/") {
			if p, err := netip.ParsePrefix(rule); err == nil {
				prefixes = append(prefixes, p)
				continue
			}
		}
		if ip, err := netip.ParseAddr(rule); err == nil {
			bits := 128
			if ip.Is4() {
				bits = 32
			}
			prefixes = append(prefixes, netip.PrefixFrom(ip, bits))
			continue
		}
		hosts[rule] = struct{}{}
	}
	return prefixes, hosts
}

func splitScopeEnv(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.FieldsFunc(v, func(r rune) bool {
		switch r {
		case ',', ';', '\n', '\t', ' ':
			return true
		default:
			return false
		}
	})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func envFirst(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

func firstToken(cmd string) string {
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return ""
	}
	return strings.ToLower(fields[0])
}

// proposalTarget extracts the remote target from a proposal for scope checking.
// Explicit tool_args["target"] always takes precedence. When falling back to
// extractTarget(), any address that looks like a local bind address (loopback,
// link-local, unspecified) is ignored — those are legitimate listen addresses,
// not remote attack targets, so scope-dropping them would be a false positive.
func proposalTarget(p Proposal) string {
	if p.ToolArgs != nil {
		if t, ok := p.ToolArgs["target"].(string); ok && strings.TrimSpace(t) != "" {
			return strings.TrimSpace(t)
		}
	}
	candidate := scope.ExtractTarget(p.Command)
	if candidate == "" {
		return ""
	}
	// If the extracted address is a loopback or link-local, skip scope check —
	// it is almost certainly a local listener/bind address (e.g. chisel, socat),
	// not the actual remote target.
	if ip, err := netip.ParseAddr(candidate); err == nil {
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			return ""
		}
	}
	return candidate
}

// classifyRisk returns a floor risk level based purely on structural command
// properties — no tool-specific flag knowledge. The model's own risk_level
// field is the primary source of truth; this function only provides a
// conservative safety floor so clearly dangerous shell operators are never
// accidentally rated low by a miscalibrated model response.
func classifyRisk(cmd string) string {
	c := strings.ToLower(cmd)

	// Structural critical signals: logical command-chaining operators that
	// expand blast radius beyond a single tool invocation.
	criticalOperators := []string{"&&", "||"}
	for _, op := range criticalOperators {
		if strings.Contains(c, op) {
			return "critical"
		}
	}

	return "low"
}

func isAlwaysBlockedIP(ip netip.Addr) bool {
	if ip.IsLoopback() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return true
	}
	if ip.Is6() {
		if ip.Is6() && ip.String() == "::1" {
			return true
		}
	}
	return false
}

func requiresApproval(risk string) bool {
	switch strings.ToLower(risk) {
	case "medium", "high", "critical":
		return true
	default:
		return false
	}
}

// isHighSeverity reports whether the given risk label is high or critical.
func isHighSeverity(risk string) bool {
	switch strings.ToLower(strings.TrimSpace(risk)) {
	case "high", "critical":
		return true
	default:
		return false
	}
}

func maxRisk(a, b string) string {
	order := map[string]int{"low": 0, "medium": 1, "high": 2, "critical": 3}
	a = strings.ToLower(strings.TrimSpace(a))
	b = strings.ToLower(strings.TrimSpace(b))
	if order[b] > order[a] {
		return b
	}
	if _, ok := order[a]; ok {
		return a
	}
	return b
}

func uniqStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
