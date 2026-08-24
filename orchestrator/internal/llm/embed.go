package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type ollamaEmbedRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type ollamaEmbedResponse struct {
	Embedding []float32 `json:"embedding"`
}

// WithEmbedModel returns a copy of the client configured to call Ollama's
// /api/embeddings endpoint with the given model tag. This is a separate model
// from the chat model (c.model) — e.g. "nomic-embed-text" vs "gpt-oss:20b".
func (c *Client) WithEmbedModel(model string) *Client {
	cp := *c
	cp.embedModel = model
	return &cp
}

// Embed returns the embedding vector for text using the configured embedding
// model. Returns an error if no embedding model is configured — callers must
// treat this as a degraded/disabled state, not a fatal error.
func (c *Client) Embed(ctx context.Context, text string) ([]float32, error) {
	if c == nil {
		return nil, fmt.Errorf("ollama client not configured")
	}
	if c.embedModel == "" {
		return nil, fmt.Errorf("embedding model not configured")
	}

	body, err := json.Marshal(ollamaEmbedRequest{Model: c.embedModel, Prompt: text})
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama embed request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama embed http %d: %s", resp.StatusCode, string(b))
	}

	var result ollamaEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode embed response: %w", err)
	}
	if len(result.Embedding) == 0 {
		return nil, fmt.Errorf("ollama returned an empty embedding")
	}
	return result.Embedding, nil
}
