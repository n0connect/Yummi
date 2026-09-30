package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenAIClient speaks an OpenAI-compatible /v1/chat/completions endpoint —
// OpenRouter, LM Studio, vLLM, llama.cpp server, or Ollama's own OpenAI-compat
// surface. It exists so conductor.Engine (and, through it, the graph agent
// backend, which reuses the same client — see cmd/orchestrator/main.go's
// `primaryLLM := assistant.LLM()`) can run against a cloud model instead of
// Ollama's native /api/chat protocol, without conductor's own code needing to
// know which protocol is underneath: this type and *Client both satisfy
// conductor.ChatBackend.
//
// Distinct from eino.OpenAIChatClient (internal/agent/eino/openai_llm.go),
// which is deliberately narrower (agent.LLM's single Chat method only, no
// history/streaming) and backs a different, single-purpose caller. This one
// needs the fuller surface conductor.Engine actually calls.
type OpenAIClient struct {
	baseURL string
	model   string
	apiKey  string
	numCtx  int
	http    *http.Client
}

// NewOpenAIClient builds a client against baseURL, which must include any
// path prefix the target server needs (e.g. "https://openrouter.ai/api/v1").
func NewOpenAIClient(baseURL, model, apiKey string, numCtx int, timeout time.Duration) *OpenAIClient {
	if timeout <= 0 {
		timeout = 180 * time.Second
	}
	return &OpenAIClient{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		model:   model,
		apiKey:  apiKey,
		numCtx:  numCtx,
		http:    &http.Client{Timeout: timeout},
	}
}

// Model returns the configured model tag — conductor surfaces this in
// references/status, so it must reflect whatever is actually answering, not
// a stale Ollama tier name.
func (c *OpenAIClient) Model() string {
	if c == nil {
		return ""
	}
	return c.model
}

func (c *OpenAIClient) isOllamaCompat() bool {
	return (c.apiKey == "" || c.apiKey == "ollama") &&
		!strings.Contains(c.baseURL, "openrouter.ai") &&
		!strings.Contains(c.baseURL, "api.openai.com")
}

func (c *OpenAIClient) newRequest(ctx context.Context, payload map[string]any) (*http.Request, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("openai chat: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai chat: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" && c.apiKey != "ollama" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	if strings.Contains(c.baseURL, "openrouter.ai") {
		req.Header.Set("HTTP-Referer", "https://github.com/n0connect/yummi")
		req.Header.Set("X-Title", "Yummi Pentest Conductor")
	}
	return req, nil
}

func toOpenAIMessages(system string, rest []Message) []map[string]string {
	out := make([]map[string]string, 0, len(rest)+1)
	out = append(out, map[string]string{"role": "system", "content": system})
	for _, m := range rest {
		out = append(out, map[string]string{"role": m.Role, "content": m.Content})
	}
	return out
}

// Chat implements conductor.ChatBackend.
func (c *OpenAIClient) Chat(ctx context.Context, system, user string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("openai-compatible client not configured")
	}
	return c.chatHistory(ctx, system, []Message{{Role: "user", Content: user}})
}

func (c *OpenAIClient) chatHistory(ctx context.Context, system string, history []Message) (string, error) {
	payload := map[string]any{
		"model":    c.model,
		"messages": toOpenAIMessages(system, history),
		"stream":   false,
	}
	if c.numCtx > 0 && c.isOllamaCompat() {
		payload["options"] = map[string]any{"num_ctx": c.numCtx}
	}
	req, err := c.newRequest(ctx, payload)
	if err != nil {
		return "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("openai chat: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("openai chat: http %d: %s", resp.StatusCode, string(b))
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

// ChatStream implements conductor.ChatBackend — see Client.ChatStream's doc
// comment for the general contract (onChunk may be nil; the return value is
// always the full accumulated text regardless).
func (c *OpenAIClient) ChatStream(ctx context.Context, system, user string, onChunk func(chunk string)) (string, error) {
	if c == nil {
		return "", fmt.Errorf("openai-compatible client not configured")
	}
	return c.chatHistoryStream(ctx, system, []Message{{Role: "user", Content: user}}, onChunk)
}

// ChatHistoryStream implements conductor.ChatBackend.
func (c *OpenAIClient) ChatHistoryStream(ctx context.Context, system string, history []Message, onChunk func(chunk string)) (string, error) {
	if c == nil {
		return "", fmt.Errorf("openai-compatible client not configured")
	}
	return c.chatHistoryStream(ctx, system, history, onChunk)
}

// chatHistoryStream speaks OpenAI's SSE chat-completions stream format: lines
// prefixed "data: ", each carrying one JSON delta chunk, terminated by a
// literal "data: [DONE]" line. Distinct on the wire from Ollama's own NDJSON
// stream (Client.doStream) — one raw JSON object per line, no "data: "
// prefix, no sentinel line — which is why this lives on a separate type
// rather than branching inside Client.
func (c *OpenAIClient) chatHistoryStream(ctx context.Context, system string, history []Message, onChunk func(chunk string)) (string, error) {
	payload := map[string]any{
		"model":    c.model,
		"messages": toOpenAIMessages(system, history),
		"stream":   true,
	}
	if c.numCtx > 0 && c.isOllamaCompat() {
		payload["options"] = map[string]any{"num_ctx": c.numCtx}
	}
	req, err := c.newRequest(ctx, payload)
	if err != nil {
		return "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("openai chat: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("openai chat: http %d: %s", resp.StatusCode, string(b))
	}

	var full strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			// A malformed/partial SSE line is a transport hiccup, not fatal —
			// mirrors Client.doStream's identical tolerance for Ollama's own
			// NDJSON stream.
			continue
		}
		if chunk.Error != nil {
			return "", fmt.Errorf("openai chat: %s", chunk.Error.Message)
		}
		for _, ch := range chunk.Choices {
			if ch.Delta.Content == "" {
				continue
			}
			full.WriteString(ch.Delta.Content)
			if onChunk != nil {
				onChunk(ch.Delta.Content)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read stream: %w", err)
	}
	if strings.TrimSpace(full.String()) == "" {
		return "", fmt.Errorf("openai chat: empty streamed response")
	}
	return full.String(), nil
}
