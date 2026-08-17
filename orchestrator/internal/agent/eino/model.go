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
	"time"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
)

// NewOllamaChatModel builds an Eino ChatModel pointed at Ollama's
// OpenAI-compatible endpoint (baseURL is e.g. "http://localhost:11434/v1").
// This project never talks to a cloud LLM provider — ByAzure is always
// false and there is no real APIKey requirement (Ollama ignores it),
// consistent with Yummi's privacy-first, local-only design.
//
// timeout bounds every individual model call. Found via live long-form
// testing: openai.ChatModelConfig's HTTPClient defaults to
// &http.Client{Timeout: config.Timeout}, and this constructor previously
// left Timeout at its zero value — an all-Go http.Client{Timeout: 0} means
// NO timeout at all. A single stalled call (e.g. Ollama needing to reload
// the model under a large tool-calling payload right as it was evicted for
// idle) then hangs the task's goroutine forever: runCtx is only ever
// cancelled by an explicit Stop() or process shutdown, never by anything
// time-based, so "running" with steps_used stuck at 0 was indistinguishable
// from a real slow turn. timeout<=0 falls back to a generous default so a
// genuinely slow (but alive) 14B+thinking-mode turn still completes.
//
// numCtx is injected as an Ollama-specific top-level "options.num_ctx" field
// via ExtraFields — the OpenAI chat-completions schema this client speaks has
// no such field, and internal/llm.Client's own num_ctx fix (Preload and every
// conductor chat call) only covers Ollama's *native* /api/generate and
// /api/chat endpoints. Found via live long-form testing: this backend's calls
// go through the OpenAI-compatible /v1/chat/completions endpoint and
// previously specified no options at all, so whenever Ollama needed to
// (re)apply options for that endpoint's own request stream it silently fell
// back to its own default context (observed live: 4096, not the configured
// tier's 16384) — and, since a single physical model can only be loaded with
// one num_ctx at a time, a conductor chat call in between (which DOES set
// num_ctx on the native API) could then force it back, so the effective
// context window was flip-flopping between endpoints rather than being
// reliably wrong OR right. numCtx<=0 omits the field (server default).
func NewOllamaChatModel(ctx context.Context, baseURL, modelName string, timeout time.Duration, numCtx int) (model.ToolCallingChatModel, error) {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	cfg := &openai.ChatModelConfig{
		APIKey:  "ollama", // required non-empty by the client; Ollama does not validate it
		Model:   modelName,
		BaseURL: baseURL,
		ByAzure: false,
		Timeout: timeout,
	}
	if numCtx > 0 {
		cfg.ExtraFields = map[string]any{"options": map[string]any{"num_ctx": numCtx}}
	}
	cm, err := openai.NewChatModel(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("eino: construct ollama chat model: %w", err)
	}
	return cm, nil
}
