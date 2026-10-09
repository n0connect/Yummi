package agent

import "testing"

func TestIsContextOverflowRecognizesProviderPhrasings(t *testing.T) {
	for _, msg := range []string{
		`openai chat: http 400: {"error":{"message":"This model's maximum context length is 32768 tokens.","code":"context_length_exceeded"}}`,
		"error, status code: 400, status: 400 Bad Request, message: maximum context length exceeded",
		"request exceeds the model's context window",
		"prompt is too long: 250000 tokens > 200000 maximum",
		"too many input tokens for this model",
	} {
		if !IsContextOverflow(errString(msg)) {
			t.Fatalf("overflow not recognized: %q", msg)
		}
	}
}

func TestIsContextOverflowIgnoresOtherFailures(t *testing.T) {
	for _, msg := range []string{
		"openai chat: request: context deadline exceeded",
		"ollama http 500: model unavailable",
		"error parsing tool call: invalid JSON",
		"",
	} {
		if msg == "" {
			if IsContextOverflow(nil) {
				t.Fatal("nil error must not be an overflow")
			}
			continue
		}
		if IsContextOverflow(errString(msg)) {
			t.Fatalf("non-overflow misclassified as overflow: %q", msg)
		}
	}
}

// OpenRouter's own compression rewrites prompts silently; the request must
// switch it OFF for OpenRouter and add nothing for any other provider.
func TestOpenRouterCompressionOffOnlyForOpenRouter(t *testing.T) {
	if got := OpenRouterCompressionOff("https://api.openai.com/v1"); got != nil {
		t.Fatalf("non-OpenRouter base URL must get no extension, got %v", got)
	}
	got := OpenRouterCompressionOff("https://openrouter.ai/api/v1")
	plugins, ok := got["plugins"].([]map[string]any)
	if !ok || len(plugins) != 1 || plugins[0]["id"] != "context-compression" || plugins[0]["enabled"] != false {
		t.Fatalf("OpenRouter must disable context-compression explicitly, got %v", got)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
