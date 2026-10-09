package agent

import "strings"

// overflowMarkers are the substrings that identify a model-context-window
// overflow in a provider error. OpenRouter documents the machine-readable form
// as error.metadata.error_type == "context_length_exceeded" (HTTP 400); the
// message text varies by upstream provider, so the marker list is deliberately
// a set of phrasings rather than one exact string. Matched case-insensitively
// against the full error text, which includes the provider's message.
var overflowMarkers = []string{
	"context_length_exceeded",
	"maximum context length",
	"context window",
	"prompt is too long",
	"too many input tokens",
	"exceeds the model's context",
	// Ollama's native API phrasing for the same condition.
	"exceeds the context length",
}

// IsContextOverflow reports whether err says the request exceeded the model's
// context window. Used to route an overflow to compaction instead of treating
// it like a transient failure: retrying the identical prompt can never succeed.
func IsContextOverflow(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, m := range overflowMarkers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// openRouterCompressionOff returns the request-body extension that turns OFF
// OpenRouter's own context-compression plugin. That plugin silently removes
// content from the middle of a prompt once a request is too large (documented:
// https://openrouter.ai/docs/guides/features/message-transforms). For an agent
// whose middle-of-conversation observations are evidence, a silent cut is
// worse than an explicit failure, and it makes the context the model sees
// unknowable from our own records. Yummi manages context itself (compaction in
// both backends), so the provider must not rewrite the prompt underneath it.
// Returns nil for every non-OpenRouter base URL.
func OpenRouterCompressionOff(baseURL string) map[string]any {
	if !strings.Contains(baseURL, "openrouter.ai") {
		return nil
	}
	return map[string]any{
		"plugins": []map[string]any{{"id": "context-compression", "enabled": false}},
	}
}
