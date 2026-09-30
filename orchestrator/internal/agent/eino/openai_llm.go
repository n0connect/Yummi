package eino

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	baseURL string
	model   string
	apiKey  string
	numCtx  int
	http    *http.Client
}

// NewOpenAIChatClient builds a client against baseURL, which must include
// any path prefix the target server needs (e.g. "http://localhost:1234/v1"
// or "https://openrouter.ai/api/v1").
func NewOpenAIChatClient(baseURL, model, apiKey string, numCtx int, timeout time.Duration) *OpenAIChatClient {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &OpenAIChatClient{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		model:   model,
		apiKey:  apiKey,
		numCtx:  numCtx,
		http:    &http.Client{Timeout: timeout},
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
	isOllama := (c.apiKey == "" || c.apiKey == "ollama") && !strings.Contains(c.baseURL, "openrouter.ai") && !strings.Contains(c.baseURL, "api.openai.com")
	if c.numCtx > 0 && isOllama {
		payload["options"] = map[string]any{"num_ctx": c.numCtx}
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
		return "", fmt.Errorf("openai chat: http %d", resp.StatusCode)
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
