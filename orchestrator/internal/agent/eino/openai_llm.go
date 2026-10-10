package eino

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/n0connect/yummi/orchestrator/internal/agent"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenAIChatClient is a minimal agent.LLM adapter over an OpenAI-compatible
// /v1/chat/completions endpoint — LM Studio, vLLM, llama.cpp server, or
// Ollama's own OpenAI-compat surface — for plain, non-tool-calling chat
// calls. internal/llm.Client can't be reused here: it speaks Ollama's native
// /api/chat protocol specifically, not this one.
//
// This is deliberately narrower than the ReAct loop's own model (cm, built
// via NewOllamaChatModel/eino-ext's openai component, which needs full
// tool-calling support): it backs only the single-purpose calls this
// backend makes outside the ReAct loop — currently just AuditGoalParts'
// natural-language goal-part audit (see Config.PlanLLM). main.go builds one
// of these from the same EinoModelBaseURL/EinoModelName/EinoModelNumCtx the
// ReAct loop itself uses, so an operator who points Eino at a different
// server (e.g. LM Studio instead of Ollama) gets a self-consistent backend —
// the audit call runs on the same model as everything else Eino does, not
// silently left on whatever the conductor happens to be configured with.
type OpenAIChatClient struct {
	baseURL   string
	model     string
	apiKey    string
	numCtx    int
	maxTokens int
	http      *http.Client
}

// NewOpenAIChatClient builds a client against baseURL, which must include
// any path prefix the target server needs (e.g. "http://localhost:1234/v1"
// or "https://openrouter.ai/api/v1"). maxTokens is passed straight to
// resolveMaxTokens (model.go) — <=0 uses its own default for a metered cloud
// target, uncapped for a local one.
func NewOpenAIChatClient(baseURL, model, apiKey string, numCtx, maxTokens int, timeout time.Duration) *OpenAIChatClient {
	if timeout <= 0 {
		// 60s is the right unspecified-default: this client is shared by
		// BOTH a local Ollama OpenAI-compat target and a real OpenRouter/
		// cloud override (see main.go's einoModelBaseURL derivation), and a
		// cloud call normally returns in a few seconds regardless of prompt
		// size — 60s is already generous there. A LOCAL target needs far
		// more slack (local inference genuinely takes minutes on a large
		// prompt); main.go passes that explicitly per call site
		// (einoTargetsSameOllama) rather than raising this shared default,
		// so a local-model fix never silently loosens the cloud path's own
		// failure-detection latency.
		timeout = 60 * time.Second
	}
	return &OpenAIChatClient{
		baseURL:   strings.TrimSuffix(baseURL, "/"),
		model:     model,
		apiKey:    apiKey,
		numCtx:    numCtx,
		maxTokens: maxTokens,
		http:      &http.Client{Timeout: timeout},
	}
}

// Chat implements agent.LLM.
func (c *OpenAIChatClient) Chat(ctx context.Context, system, user string) (string, error) {
	payload := map[string]any{
		"model": c.model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"stream": false,
	}
	for k, v := range agent.OpenRouterCompressionOff(c.baseURL) {
		payload[k] = v
	}
	isOllama := (c.apiKey == "" || c.apiKey == "ollama") && !strings.Contains(c.baseURL, "openrouter.ai") && !strings.Contains(c.baseURL, "api.openai.com")
	if c.numCtx > 0 && isOllama {
		payload["options"] = map[string]any{"num_ctx": c.numCtx}
	}
	if mt := resolveMaxTokens(c.baseURL, c.maxTokens); mt != nil {
		payload["max_tokens"] = *mt
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("openai chat: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("openai chat: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" && c.apiKey != "ollama" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	if strings.Contains(c.baseURL, "openrouter.ai") {
		req.Header.Set("HTTP-Referer", "https://github.com/n0connect/yummi")
		req.Header.Set("X-Title", "Yummi Pentest Conductor")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("openai chat: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		// Include a bounded slice of the provider's error body: it is where the
		// machine-readable reason (e.g. context_length_exceeded) lives, and the
		// status code alone made overflow indistinguishable from any other 400.
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("openai chat: http %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("openai chat: decode response: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("openai chat: empty choices")
	}
	return out.Choices[0].Message.Content, nil
}
