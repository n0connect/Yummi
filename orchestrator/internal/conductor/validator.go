package conductor

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
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

// Validator enforces risk classification and approval gating for assistant
// proposals. It no longer makes scope decisions — gateway is the sole
// authority on whether a target is reachable.
type Validator struct{}

// NewValidator creates a Validator. Validator no longer makes scope
// decisions — gateway is the sole authority on whether a target is
// reachable. Validator now only classifies and gates on risk level.
func NewValidator() *Validator {
	return &Validator{}
}

// ValidateChatResponse normalizes proposals and classifies risk. It does not
// make scope decisions — gateway is the only component that knows whether a
// target is reachable; that's discovered at execution time
// (executor.RunScopedCommand), not here.
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
			resp.Message = "No safe proposal could be prepared. Please share the desired action."
		}
		if resp.Confidence > 0.5 {
			resp.Confidence = 0.5
		}
	}

	return resp
}

func firstToken(cmd string) string {
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return ""
	}
	return strings.ToLower(fields[0])
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
