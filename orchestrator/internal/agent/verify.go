package agent

import "strings"

// Outcome classifies a finished command for step bookkeeping and tagging.
type Outcome int

const (
	OutcomeSuccess Outcome = iota
	OutcomeFailure
)

// failurePatterns downgrade a zero-exit result to failure. They are
// conservative substring checks (lowercased) covering the common cases where
// a tool exits 0 but did nothing useful. They never upgrade a non-zero exit.
var failurePatterns = []string{
	"command not found",
	"0 hosts up",
	"no such file or directory",
	"connection refused",
	"name or service not known",
	"permission denied",
}

// Verify decides success/failure from a ToolResult: exit-state first, then
// conservative output patterns. The returned string is a human reason on
// failure (empty on success).
func Verify(result ToolResult) (Outcome, string) {
	if result.IsError {
		reason := strings.TrimSpace(firstLine(result.Content))
		if reason == "" {
			reason = "command exited with an error"
		}
		return OutcomeFailure, reason
	}
	low := strings.ToLower(result.Content)
	for _, p := range failurePatterns {
		if strings.Contains(low, p) {
			return OutcomeFailure, "output indicates failure: " + p
		}
	}
	return OutcomeSuccess, ""
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
