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
	"strings"
	"time"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
)

// NewOllamaChatModel builds an Eino ChatModel pointed at an OpenAI-compatible
// endpoint (Ollama, LM Studio, vLLM, OpenAI, or OpenRouter).
//
// timeout bounds every individual model call.
func NewOllamaChatModel(ctx context.Context, baseURL, modelName, apiKey string, timeout time.Duration, numCtx int, reasoning bool) (model.ToolCallingChatModel, error) {
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
	if len(extra) > 0 {
		cfg.ExtraFields = extra
	}
	cm, err := openai.NewChatModel(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("eino: construct chat model: %w", err)
	}
	return cm, nil
}
