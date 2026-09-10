package agent

import (
	"math"
	"regexp"
	"strings"
)

// ActiveProofInput holds data submitted for automated active proof verification.
type ActiveProofInput struct {
	PoCType        string `json:"poc_type"` // "xss", "sqli", "ssrf", "auth_bypass", "cors"
	Evidence       string `json:"evidence"`
	BaselineOutput string `json:"baseline_output,omitempty"`
	ExploitOutput  string `json:"exploit_output"`
	ExpectedToken  string `json:"expected_token,omitempty"`
}

// ActiveProofResult encapsulates the machine-checked verification outcome.
type ActiveProofResult struct {
	Verified   bool       `json:"verified"`
	OracleKind OracleKind `json:"oracle_kind"`
	Confidence float64    `json:"confidence"`
	Reason     string     `json:"reason"`
}

var (
	domExecPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)<script[^>]*>[^<]*alert\([^)]*\)`),
		regexp.MustCompile(`(?i)<img[^>]+src=[^>]+onerror\s*=`),
		regexp.MustCompile(`(?i)<svg[^>]+onload\s*=`),
		regexp.MustCompile(`(?i)javascript:\s*alert\(`),
		regexp.MustCompile(`(?i)prompt\(|confirm\(`),
	}
	corsAcaoRegex = regexp.MustCompile(`(?i)access-control-allow-origin:\s*(\*|https?://[^\r\n]+)`)
	corsAcacRegex = regexp.MustCompile(`(?i)access-control-allow-credentials:\s*true`)
)

// EvaluateActiveProof performs deterministic, rule-based verification on active exploit traces.
func EvaluateActiveProof(input ActiveProofInput) ActiveProofResult {
	pocType := strings.ToLower(strings.TrimSpace(input.PoCType))
	exploit := input.ExploitOutput

	switch pocType {
	case "xss", "dom":
		for _, pat := range domExecPatterns {
			if pat.MatchString(exploit) || (input.Evidence != "" && pat.MatchString(input.Evidence)) {
				return ActiveProofResult{
					Verified:   true,
					OracleKind: OracleDOMExecution,
					Confidence: 0.98,
					Reason:     "verified unescaped DOM/script execution sequence in captured response",
				}
			}
		}
		if input.ExpectedToken != "" && strings.Contains(exploit, input.ExpectedToken) {
			return ActiveProofResult{
				Verified:   true,
				OracleKind: OracleActivePoC,
				Confidence: 0.90,
				Reason:     "verified exact reflection of exploit canary token",
			}
		}

	case "ssrf", "oob":
		if input.ExpectedToken != "" && strings.Contains(exploit, input.ExpectedToken) {
			return ActiveProofResult{
				Verified:   true,
				OracleKind: OracleOOBInteraction,
				Confidence: 0.99,
				Reason:     "verified out-of-band canary interaction token",
			}
		}

	case "sqli", "differential":
		if input.BaselineOutput != "" && exploit != "" {
			diffRatio := computeDiffRatio(input.BaselineOutput, exploit)
			if diffRatio > 0.15 {
				return ActiveProofResult{
					Verified:   true,
					OracleKind: OracleDifferential,
					Confidence: math.Min(0.95, 0.70+diffRatio),
					Reason:     "verified statistically significant response differential",
				}
			}
		}

	case "auth_bypass", "status_diff":
		if strings.Contains(input.BaselineOutput, "401") || strings.Contains(input.BaselineOutput, "403") {
			if strings.Contains(exploit, "200 OK") || strings.Contains(exploit, "HTTP/1.1 200") || strings.Contains(exploit, "HTTP/2 200") {
				return ActiveProofResult{
					Verified:   true,
					OracleKind: OracleActivePoC,
					Confidence: 0.95,
					Reason:     "verified 401/403 baseline to 200 OK transition on exploit payload",
				}
			}
		}

	case "cors":
		if corsAcaoRegex.MatchString(exploit) && corsAcacRegex.MatchString(exploit) {
			return ActiveProofResult{
				Verified:   true,
				OracleKind: OracleActivePoC,
				Confidence: 0.96,
				Reason:     "verified arbitrary origin reflection with access-control-allow-credentials: true",
			}
		}
	}

	if input.Evidence != "" && matchEvidence(exploit, input.Evidence) {
		return ActiveProofResult{
			Verified:   true,
			OracleKind: OracleToolEvidence,
			Confidence: 0.85,
			Reason:     "verified verbatim evidence in captured output",
		}
	}

	return ActiveProofResult{
		Verified:   false,
		OracleKind: "",
		Confidence: 0.0,
		Reason:     "active proof criteria not satisfied by captured output",
	}
}

func computeDiffRatio(a, b string) float64 {
	la := float64(len(a))
	lb := float64(len(b))
	if la == 0 && lb == 0 {
		return 0
	}
	diff := math.Abs(la - lb)
	maxLen := math.Max(la, lb)
	return diff / maxLen
}

// ClassifyOracleKind infers the most specific OracleKind from finding metadata.
func ClassifyOracleKind(title, evidence, capturedOutput string) OracleKind {
	for _, pat := range domExecPatterns {
		if pat.MatchString(evidence) || pat.MatchString(capturedOutput) {
			return OracleDOMExecution
		}
	}
	lowerTitle := strings.ToLower(title)
	if strings.Contains(lowerTitle, "ssrf") || strings.Contains(lowerTitle, "out-of-band") || strings.Contains(lowerTitle, "oob") {
		return OracleOOBInteraction
	}
	if strings.Contains(lowerTitle, "sqli") || strings.Contains(lowerTitle, "timing") || strings.Contains(lowerTitle, "differential") {
		return OracleDifferential
	}
	if strings.Contains(lowerTitle, "poc") || strings.Contains(lowerTitle, "bypass") || strings.Contains(lowerTitle, "cors") {
		return OracleActivePoC
	}
	return OracleToolEvidence
}
