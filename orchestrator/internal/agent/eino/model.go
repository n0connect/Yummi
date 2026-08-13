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

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
)

// NewOllamaChatModel builds an Eino ChatModel pointed at Ollama's
// OpenAI-compatible endpoint (baseURL is e.g. "http://localhost:11434/v1").
// This project never talks to a cloud LLM provider — ByAzure is always
// false and there is no real APIKey requirement (Ollama ignores it),
// consistent with Yummi's privacy-first, local-only design.
func NewOllamaChatModel(ctx context.Context, baseURL, modelName string) (model.ToolCallingChatModel, error) {
	cm, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		APIKey:  "ollama", // required non-empty by the client; Ollama does not validate it
		Model:   modelName,
		BaseURL: baseURL,
		ByAzure: false,
	})
	if err != nil {
		return nil, fmt.Errorf("eino: construct ollama chat model: %w", err)
	}
	return cm, nil
}
