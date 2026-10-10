// Package eino implements a second agentic backend on top of CloudWeGo's
// Eino ADK, for operators running larger/native-tool-calling-capable local
// models. It reuses every security primitive the graph engine
// (orchestrator/internal/agent) already has — Execer, Verify, threatscan,
// Gate, conductor.Validator — so there is no second execution path and no
// new attack surface beyond the Eino dependency itself. Selected at
// build/runtime via YUMMI_AGENT_BACKEND (see cmd/orchestrator/main.go).
package eino

import (
	"context"
	"fmt"
	"github.com/n0connect/yummi/orchestrator/internal/agent"
	"strings"
	"time"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
)

// defaultCloudMaxTokens caps a single ReAct turn's completion size against a
// metered cloud provider when the operator hasn't set an explicit override.
// Leaving max_tokens unset entirely does NOT mean "let the provider pick
// something reasonable" — OpenRouter defaults it to the model's absolute
// output ceiling (e.g. 65536 for gpt-4.1) and rejects the call outright if
// the key's remaining credit can't cover that worst case, even though a
// real turn's response (a short answer, or a few tool-call JSON lines) never
// comes close to needing it. Live-observed: gpt-4.1 via OpenRouter failed
// every task's very first turn with HTTP 402 ("requested up to 65536
// tokens, but can only afford 32563") purely from this default, with ample
// balance for what the turn actually would have produced. 8192 comfortably
// covers any real response this backend produces while staying far under
// typical per-key credit limits — see resolveMaxTokens.
const defaultCloudMaxTokens = 8192

// resolveMaxTokens returns the max_tokens value to send, or nil to omit the
// field entirely. explicit > 0 (an operator-set override, any provider)
// always wins; otherwise a metered cloud target (OpenRouter/OpenAI) gets
// defaultCloudMaxTokens, and a local target (Ollama/LM Studio/vLLM/...) is
// left uncapped — it's already bounded by its own context window and isn't
// billed per token, so there's nothing this would protect against there.
func resolveMaxTokens(baseURL string, explicit int) *int {
	if explicit > 0 {
		v := explicit
		return &v
	}
	if strings.Contains(baseURL, "openrouter.ai") || strings.Contains(baseURL, "api.openai.com") {
		v := defaultCloudMaxTokens
		return &v
	}
	return nil
}

// NewOllamaChatModel builds an Eino ChatModel pointed at an OpenAI-compatible
// endpoint (Ollama, LM Studio, vLLM, OpenAI, or OpenRouter).
//
// timeout bounds every individual model call. maxTokens <=0 uses
// resolveMaxTokens's own default for a metered cloud target (see its doc
// comment); pass a positive value to override it for any provider.
func NewOllamaChatModel(ctx context.Context, baseURL, modelName, apiKey string, timeout time.Duration, numCtx, maxTokens int, reasoning bool) (model.ToolCallingChatModel, error) {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	if apiKey == "" {
		apiKey = "ollama"
	}
	// Temperature: 0 for deterministic output.
	zeroTemp := float32(0)
	cfg := &openai.ChatModelConfig{
		APIKey:      apiKey,
		Model:       modelName,
		BaseURL:     baseURL,
		ByAzure:     false,
		Timeout:     timeout,
		Temperature: &zeroTemp,
		MaxTokens:   resolveMaxTokens(baseURL, maxTokens),
	}
	extra := map[string]any{}
	// Only inject Ollama-specific options when talking to Ollama
	isOllama := apiKey == "ollama" && !strings.Contains(baseURL, "openrouter.ai") && !strings.Contains(baseURL, "api.openai.com")
	if numCtx > 0 && isOllama {
		extra["options"] = map[string]any{"num_ctx": numCtx}
	}
	if reasoning {
		extra["reasoning_effort"] = "high"
	}
	if strings.Contains(baseURL, "openrouter.ai") {
		extra["headers"] = map[string]string{
			"HTTP-Referer": "https://github.com/n0connect/yummi",
			"X-Title":      "Yummi Pentest Conductor",
		}
	}
	// Yummi owns context management on this backend; OpenRouter must not
	// silently rewrite the prompt (see agent.OpenRouterCompressionOff).
	for k, v := range agent.OpenRouterCompressionOff(baseURL) {
		extra[k] = v
	}
	if len(extra) > 0 {
		cfg.ExtraFields = extra
	}
	cm, err := openai.NewChatModel(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("eino: construct chat model: %w", err)
	}
	return cm, nil
}
