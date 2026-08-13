package skillcurator

import (
	"regexp"
	"strings"

	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
	"github.com/n0connect/yummi/orchestrator/internal/security/threatscan"
)

var (
	ipv4Pattern   = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	urlPattern    = regexp.MustCompile(`(?i)\bhttps?://[^\s"']+`)
	emailPattern  = regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b`)
	secretPattern = regexp.MustCompile(`(?i)\b(password|passwd|token|api[_-]?key|secret|authorization)\s*[:=]\s*[^\s,;]+`)
)

// SanitizeTrajectory removes engagement identifiers and credentials before a
// successful task is generalized into a reusable skill. Target-scrubbed
// tactics are retained; raw observations are bounded and hostile prompt text
// is dropped instead of being handed to the curator model.
func SanitizeTrajectory(in Trajectory) Trajectory {
	out := in
	out.Goal = sanitizeTrajectoryText(in.Goal, 1200)
	out.Summary = sanitizeTrajectoryText(in.Summary, 2400)
	out.Observations = make([]string, 0, min(len(in.Observations), 20))
	for _, observation := range in.Observations {
		if len(out.Observations) == 20 {
			break
		}
		clean := sanitizeTrajectoryText(observation, 1000)
		if clean != "" {
			out.Observations = append(out.Observations, clean)
		}
	}
	// Tactics originate in knowledge.Store after target scrubbing and remain
	// the preferred reusable signal. Bound notes/tags defensively anyway.
	out.Tactics = append([]knowledge.Tactic(nil), in.Tactics...)
	for i := range out.Tactics {
		out.Tactics[i].Note = sanitizeTrajectoryText(out.Tactics[i].Note, 500)
		if len(out.Tactics[i].ContextTags) > 16 {
			out.Tactics[i].ContextTags = out.Tactics[i].ContextTags[:16]
		}
	}
	return out
}

func sanitizeTrajectoryText(text string, maxRunes int) string {
	text = threatscan.StripANSI(strings.TrimSpace(text))
	text = urlPattern.ReplaceAllString(text, "<TARGET_URL>")
	text = ipv4Pattern.ReplaceAllString(text, "<TARGET_IP>")
	text = emailPattern.ReplaceAllString(text, "<EMAIL>")
	text = secretPattern.ReplaceAllString(text, "$1=<REDACTED>")
	if threatscan.FirstThreatMessage(text, threatscan.ScopeStrict) != "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) > maxRunes {
		text = string(runes[:maxRunes]) + "…"
	}
	return text
}
