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
//
// reasoning mirrors conductor.TierOverrides.Thinking's intent, which
// previously never reached this backend at all: tier.go declares
// Thinking:false for every gpt-oss tier, but that value was only ever
// threaded into the conductor's native-API calls (internal/llm.Options.Think,
// a Qwen3-shaped boolean) — this OpenAI-compat client set no reasoning
// signal whatsoever, leaving gpt-oss's actual effort level to whatever
// Ollama defaults to server-side when the field is omitted (confirmed live
// via a raw /v1/chat/completions call: the model still reasons by default,
// just at an undeclared, uncontrolled level — this was never a "thinking
// completely off" gap, only an unconfigured one).
//
// reasoning=false OMITS the field entirely rather than forcing an explicit
// "medium" — tried exactly that first, and it regressed a real long-form
// task: at step 7 of a 78-step budget, with a full ~20-tool schema plus six
// prior tool-output turns already in context, the model's tool-call JSON
// got cut off mid-argument (`{"rationale":"Fingerprint` — unexpected end of
// JSON input) and the task hard-failed. eino-ext's MaxTokens is never set by
// this client (nil, omitted — see cfg construction below), so nothing caps
// generation length explicitly; forcing reasoning_effort spends more of
// Ollama's own context-aware generation ceiling on the reasoning channel
// before the model gets to writing the actual tool-call arguments, and that
// ceiling shrinks as conversation history grows across steps — exactly why
// this surfaced deep into a task, not on turn one. Given both gpt-oss tiers
// currently resolve Thinking:false, and the server default already reasons
// adequately (confirmed above) with zero observed truncation before this
// field was added, omission is the proven-safe choice for the common case.
// reasoning=true still maps to an explicit "high" — an opt-in a future
// caller can reach for deliberately, not the default path every task takes.
func NewOllamaChatModel(ctx context.Context, baseURL, modelName string, timeout time.Duration, numCtx int, reasoning bool) (model.ToolCallingChatModel, error) {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	// Temperature: 0 — same reasoning llm.DefaultQwen3Options already applies
	// to the conductor's own native-API calls (deterministic output matters
	// more than sampling diversity for a security tool deciding what command
	// to run), but this OpenAI-compat client left it unset entirely, so every
	// decision in the ReAct loop — including whether to call finish or keep
	// working — sampled from Ollama's own default temperature instead. Found
	// while investigating a real task where the model gave up prematurely
	// after a nudge: isolated replays of the exact same nudge, at whatever
	// temperature Ollama defaults to, correctly picked the right next tool
	// every time they were tried standalone, meaning the real run's outcome
	// wasn't a reliable failure mode — just one of several possible samples.
	// Pinning this doesn't replace the structural nudge-enforcement below
	// (a model can still legitimately reason its way to a wrong call), but
	// removes randomness as a source of run-to-run inconsistency on top of it.
	zeroTemp := float32(0)
	cfg := &openai.ChatModelConfig{
		APIKey:      "ollama", // required non-empty by the client; Ollama does not validate it
		Model:       modelName,
		BaseURL:     baseURL,
		ByAzure:     false,
		Timeout:     timeout,
		Temperature: &zeroTemp,
	}
	extra := map[string]any{}
	if numCtx > 0 {
		extra["options"] = map[string]any{"num_ctx": numCtx}
	}
	if reasoning {
		extra["reasoning_effort"] = "high"
	}
	if len(extra) > 0 {
		cfg.ExtraFields = extra
	}
	cm, err := openai.NewChatModel(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("eino: construct ollama chat model: %w", err)
	}
	return cm, nil
}
