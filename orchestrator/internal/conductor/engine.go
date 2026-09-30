package conductor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/n0connect/yummi/orchestrator/internal/llm"
)

// ChatBackend is everything Engine needs from its LLM client — satisfied by
// both *llm.Client (Ollama's native /api/chat) and *llm.OpenAIClient (any
// OpenAI-compatible /v1/chat/completions endpoint, e.g. OpenRouter). Engine
// doesn't care which protocol is underneath; NewEngineWithOverrides decides
// that once at construction time based on whether a cloud override is
// configured. Kept here (the consumer) rather than in package llm, per the
// usual Go "accept interfaces" placement.
type ChatBackend interface {
	Model() string
	Chat(ctx context.Context, system, user string) (string, error)
	ChatStream(ctx context.Context, system, user string, onChunk func(chunk string)) (string, error)
	ChatHistoryStream(ctx context.Context, system string, history []llm.Message, onChunk func(chunk string)) (string, error)
}

// Engine coordinates persona-backed assistant decisions via LLM.
// A nil llmClient means no backend is configured; Chat returns decline.
type Engine struct {
	persona   Persona
	validator *Validator
	llmClient ChatBackend
	tier      TierConfig

	histMu      sync.Mutex
	history     map[string][]llm.Message // keyed by session ID
	turnMu      sync.Mutex
	activeTurns map[string]string

	// stream fans out raw display-token deltas (see stream.go) while a chat
	// call is in flight. Zero-value-usable — no constructor wiring needed.
	stream streamBroker
}

var ErrTurnInProgress = errors.New("turn already in progress for session")

// TurnInProgressError identifies the active turn that owns a session's LLM
// history lease. The API maps this to HTTP 409 without mutating history.
type TurnInProgressError struct {
	SessionID    string
	ActiveTurnID string
}

func (e *TurnInProgressError) Error() string {
	return fmt.Sprintf("%s: session=%s active_turn_id=%s", ErrTurnInProgress, e.SessionID, e.ActiveTurnID)
}

func (e *TurnInProgressError) Is(target error) bool { return target == ErrTurnInProgress }

// maxHistoryPerSession caps stored messages (not user/assistant pairs) to
// bound memory while still supporting the documented 20–50 message journeys.
// The Pro tier's real 16K preload is verified separately in internal/llm.
const maxHistoryPerSession = 50

// capHistory trims msgs to the most recent maxHistoryPerSession messages (the
// tail). Shared by every write path that appends to a session's in-memory
// history (ChatWithHistory, SeedHistory, AppendObservation) so the cap is
// enforced identically everywhere.
func capHistory(msgs []llm.Message) []llm.Message {
	if len(msgs) > maxHistoryPerSession {
		return msgs[len(msgs)-maxHistoryPerSession:]
	}
	return msgs
}

// TierOverrides lets an operator override the numeric knobs a resolved tier
// profile would otherwise apply. Most useful for a custom model tag (see
// TierRegistry.Resolve) that isn't one of the two named tiers: it's wrapped
// under Standard's fixed 32K-context/no-thinking profile by default, which
// under-serves a large-context model (e.g. an operator with the hardware for
// a big MoE model wanting its full context window instead of being capped at
// Standard's). Zero/nil fields mean "use the resolved tier's own value" — this is
// additive, never a replacement for the tier system. Deliberately excludes
// MaxTokens: TierConfig.MaxTokens isn't wired into any actual request
// parameter anywhere in internal/llm today (a pre-existing gap, not
// introduced here) — adding an override for a knob that does nothing would
// be misleading.
type TierOverrides struct {
	NumCtx   int   // 0 = tier default
	Thinking *bool // nil = tier default

	// CloudBaseURL/CloudModel/CloudAPIKey, when CloudBaseURL is non-empty,
	// make the conductor talk to an OpenAI-compatible endpoint (e.g.
	// OpenRouter) INSTEAD of Ollama — not an addition alongside it. This is
	// a deliberate, mandatory replacement: an operator who has already
	// opted an agent backend into a cloud model gets zero benefit from also
	// running a redundant local model purely for chat/intent
	// classification, since that classification step already hands the
	// same goal text to the same cloud model the moment a task starts. See
	// ChatBackend's doc comment.
	CloudBaseURL string
	CloudModel   string
	CloudAPIKey  string
}

// NewEngine constructs an engine from persona files and optional Ollama settings.
// When ollamaURL is empty, the engine declines all requests with a clear message.
func NewEngine(personaDir, ollamaURL, ollamaModel string) (*Engine, error) {
	return NewEngineWithOverrides(personaDir, ollamaURL, ollamaModel, TierOverrides{})
}

// NewEngineWithOverrides is NewEngine plus explicit tier-knob overrides —
// see TierOverrides. NewEngine is a thin wrapper over this with zero
// overrides, kept so every existing caller (main.go, dev harnesses, tests)
// is unaffected.
func NewEngineWithOverrides(personaDir, ollamaURL, ollamaModel string, overrides TierOverrides) (*Engine, error) {
	persona, err := LoadPersona(resolvePersonaDir(personaDir))
	if err != nil {
		slog.Warn("persona load failed; continuing with empty persona",
			"error", err, "persona_dir", personaDir)
		persona = Persona{}
	}

	tier := DefaultTierRegistry().Resolve(ollamaModel)
	if overrides.NumCtx > 0 {
		tier.NumCtx = overrides.NumCtx
	}
	if overrides.Thinking != nil {
		tier.Thinking = *overrides.Thinking
	}

	// var client ChatBackend (not *llm.Client): a *llm.Client(nil) assigned
	// into an interface field is a classic Go trap — the interface value
	// would be non-nil (it has a type) even though the pointer inside is
	// nil, breaking every "e.llmClient == nil" nil-safety check elsewhere
	// in this file. Starting from the interface's own zero value and only
	// ever assigning a genuinely-constructed client keeps a true nil when
	// neither branch below fires.
	var client ChatBackend
	if overrides.CloudBaseURL != "" {
		client = llm.NewOpenAIClient(overrides.CloudBaseURL, overrides.CloudModel, overrides.CloudAPIKey, tier.NumCtx, 0)
	} else if ollamaURL != "" {
		opts := llm.DefaultQwen3Options()
		opts.NumCtx = tier.NumCtx
		if tier.Thinking {
			t := true
			opts.Think = &t
		}
		client = llm.NewClient(ollamaURL, tier.Model).WithOptions(opts)
	}

	return &Engine{
		persona:     persona,
		validator:   NewValidator(),
		llmClient:   client,
		tier:        tier,
		history:     make(map[string][]llm.Message),
		activeTurns: make(map[string]string),
	}, nil
}

func (e *Engine) acquireTurn(sessionID, turnID string) error {
	e.turnMu.Lock()
	defer e.turnMu.Unlock()
	if e.activeTurns == nil {
		e.activeTurns = make(map[string]string)
	}
	if active, ok := e.activeTurns[sessionID]; ok {
		return &TurnInProgressError{SessionID: sessionID, ActiveTurnID: active}
	}
	e.activeTurns[sessionID] = turnID
	return nil
}

func (e *Engine) releaseTurn(sessionID, turnID string) {
	e.turnMu.Lock()
	defer e.turnMu.Unlock()
	if e.activeTurns[sessionID] == turnID {
		delete(e.activeTurns, sessionID)
	}
}

// Tier returns the resolved model tier configuration.
func (e *Engine) Tier() TierConfig {
	if e == nil {
		return TierConfig{}
	}
	return e.tier
}

// PersonaVersion returns the loaded persona version.
func (e *Engine) PersonaVersion() string {
	if e == nil {
		return ""
	}
	return e.persona.Version
}

// OllamaModel returns the configured model tag (Ollama or, when a cloud
// override replaced it, the cloud model — see ChatBackend), or empty when no
// backend is configured. e.llmClient == nil must be checked explicitly here:
// unlike the old concrete *llm.Client, whose own Model() method was
// nil-receiver-safe, a genuinely nil ChatBackend interface value has no
// concrete type to dispatch to at all, and calling any method on it panics
// rather than degrading gracefully — confirmed live by
// TestEngineOllamaModelReporting against an unconfigured engine.
func (e *Engine) OllamaModel() string {
	if e == nil || e.llmClient == nil {
		return ""
	}
	return e.llmClient.Model()
}

// Backend reports which protocol e.llmClient actually speaks — "ollama",
// "cloud" (an OpenAI-compatible endpoint, e.g. OpenRouter — see
// TierOverrides.CloudBaseURL), or "none" when unconfigured. Exists so
// status/health reporting (api/system.go) doesn't hardcode "ollama" the way
// it did before ChatBackend existed, which kept claiming "ollama" even after
// a cloud override had replaced it — confirmed live: /api/v1/system/status
// showed backend:"ollama" while meta.model correctly reported the OpenRouter
// model tag, a real, user-visible inconsistency.
func (e *Engine) Backend() string {
	if e == nil || e.llmClient == nil {
		return "none"
	}
	if _, ok := e.llmClient.(*llm.OpenAIClient); ok {
		return "cloud"
	}
	return "ollama"
}

// LLM exposes the underlying client for non-chat callers (agent runner).
// Nil when Ollama is not configured.
func (e *Engine) LLM() ChatBackend {
	if e == nil {
		return nil
	}
	return e.llmClient
}

// Validator exposes the scope/risk validator for non-chat callers.
func (e *Engine) Validator() *Validator {
	if e == nil {
		return nil
	}
	return e.validator
}

// Chat produces a structured response for a single user message.
// Returns decline immediately when Ollama is not configured.
func (e *Engine) Chat(ctx context.Context, message string) ChatResponse {
	text := strings.TrimSpace(message)
	if decision, ok := BoundaryDecision(text); ok {
		return e.finalize(decision)
	}

	if e.llmClient == nil {
		return e.finalize(ChatResponse{
			Intent: "decline",
			Message: localize(text,
				"LLM bağlantısı yapılandırılmamış. Lütfen YUMMI_OLLAMA_URL ortam değişkenini ayarlayın ve Ollama'nın çalıştığından emin olun.",
				"LLM backend not configured. Please set YUMMI_OLLAMA_URL and ensure Ollama is running."),
			Confidence: 1.0,
			References: []string{"config:ollama_required"},
		})
	}

	resp, err := e.chatWithLLM(ctx, "", text)
	if err != nil {
		slog.Warn("ollama chat failed", "error", err)
		return e.finalize(ChatResponse{
			Intent: "decline",
			Message: localize(text,
				"Ollama ile bağlantı kurulamadı. Servisin çalıştığından emin olun.",
				"Failed to reach Ollama. Please ensure the service is running."),
			Confidence: 1.0,
			References: []string{"llm:connection_error"},
		})
	}
	return resp
}

// ChatWithHistory is like Chat but maintains per-session conversation context.
func (e *Engine) ChatWithHistory(ctx context.Context, sessionID, message string) ChatResponse {
	resp, err := e.ChatWithHistoryTurn(ctx, sessionID, "", message)
	if err != nil {
		return e.finalize(ChatResponse{Intent: "decline", Message: err.Error(), Confidence: 1})
	}
	return resp
}

// ChatWithHistoryTurn serializes LLM history mutation per session and tags
// every cosmetic stream event with the caller's turn ID. Different sessions
// remain fully concurrent.
func (e *Engine) ChatWithHistoryTurn(ctx context.Context, sessionID, turnID, message string) (ChatResponse, error) {
	return e.chatWithHistoryTurn(ctx, sessionID, turnID, message, true)
}

// ChatWithHistoryFeedbackTurn processes a tool-output turn generated by the
// operator UI. Tool output is untrusted data, not a new operator request, so
// operator-only target clarification policy must not inspect it. The normal
// history serialization and context cap still apply.
func (e *Engine) ChatWithHistoryFeedbackTurn(ctx context.Context, sessionID, turnID, message string) (ChatResponse, error) {
	return e.chatWithHistoryTurn(ctx, sessionID, turnID, message, false)
}

func (e *Engine) chatWithHistoryTurn(ctx context.Context, sessionID, turnID, message string, enforceOperatorBoundary bool) (ChatResponse, error) {
	text := strings.TrimSpace(message)
	if enforceOperatorBoundary {
		if decision, ok := BoundaryDecision(text); ok {
			return e.finalize(decision), nil
		}
	}

	if e.llmClient == nil || sessionID == "" {
		if enforceOperatorBoundary {
			return e.Chat(ctx, text), nil
		}
		if e.llmClient == nil {
			return e.finalize(ChatResponse{
				Intent: "decline",
				Message: localize(text,
					"LLM bağlantısı yapılandırılmamış. Lütfen YUMMI_OLLAMA_URL ortam değişkenini ayarlayın ve Ollama'nın çalıştığından emin olun.",
					"LLM backend not configured. Please set YUMMI_OLLAMA_URL and ensure Ollama is running."),
				Confidence: 1.0,
				References: []string{"config:ollama_required"},
			}), nil
		}
		resp, err := e.chatWithLLM(ctx, "", text)
		if err != nil {
			return e.finalize(ChatResponse{
				Intent: "decline",
				Message: localize(text,
					"Ollama ile bağlantı kurulamadı. Servisin çalıştığından emin olun.",
					"Failed to reach Ollama. Please ensure the service is running."),
				Confidence: 1.0,
				References: []string{"llm:connection_error"},
			}), nil
		}
		return resp, nil
	}
	if err := e.acquireTurn(sessionID, turnID); err != nil {
		return ChatResponse{}, err
	}
	defer e.releaseTurn(sessionID, turnID)

	e.histMu.Lock()
	msgs := capHistory(append(e.history[sessionID], llm.Message{Role: "user", Content: text}))
	e.history[sessionID] = msgs
	e.histMu.Unlock()

	resp, err := e.chatWithLLMHistory(ctx, sessionID, turnID, msgs)
	if err != nil {
		slog.Warn("ollama history chat failed", "error", err, "session", sessionID)
		return e.Chat(ctx, text), nil
	}

	e.histMu.Lock()
	e.history[sessionID] = append(e.history[sessionID], llm.Message{Role: "assistant", Content: resp.Message})
	e.histMu.Unlock()

	return resp, nil
}

// ClearHistory removes the conversation history for a session.
func (e *Engine) ClearHistory(sessionID string) {
	e.histMu.Lock()
	delete(e.history, sessionID)
	e.histMu.Unlock()
}

// SeedHistory primes a session's in-memory chat history from persisted turns —
// but ONLY when it is currently empty, so a process restart restores prior
// context without ever clobbering a live in-memory conversation. The slice is
// capped to the most recent maxHistoryPerSession turns (the tail). This is what
// makes durable chat history actually feed the model after a restart, instead
// of every reloaded conversation starting cold. Safe on nil engine, empty
// session, disabled LLM, or empty input.
func (e *Engine) SeedHistory(sessionID string, msgs []llm.Message) {
	if e == nil || e.llmClient == nil || sessionID == "" || len(msgs) == 0 {
		return
	}
	e.histMu.Lock()
	defer e.histMu.Unlock()
	if len(e.history[sessionID]) > 0 {
		return // live history already present — don't double-seed
	}
	e.history[sessionID] = capHistory(append([]llm.Message(nil), msgs...))
}

// ── LLM helpers ──────────────────────────────────────────────────────────────

func (e *Engine) chatWithLLMHistory(ctx context.Context, sessionID, turnID string, history []llm.Message) (ChatResponse, error) {
	system := e.buildSystemPrompt()
	var buf strings.Builder
	sent := 0
	raw, err := e.llmClient.ChatHistoryStream(ctx, system, history, func(chunk string) {
		buf.WriteString(chunk)
		if preview := extractMessagePreview(buf.String()); len(preview) > sent {
			e.publishStream(sessionID, turnID, preview[sent:], false)
			sent = len(preview)
		}
	})
	e.publishStream(sessionID, turnID, "", true)
	if err != nil {
		return ChatResponse{}, err
	}

	if resp, ok := parseStructuredResponse(raw); ok {
		return e.validator.ValidateChatResponse(resp), nil
	}

	return e.validator.ValidateChatResponse(ChatResponse{
		Intent:     "llm_response",
		Message:    raw,
		Confidence: 0.75,
		References: []string{"llm:" + e.llmClient.Model()},
	}), nil
}

// chatWithLLM sends the message to Ollama, retries on parse failure, and
// issues an intent-rescue retry when the model returns explain with no proposals
// for an actionable request. Only the primary attempt streams display tokens
// (via sessionID, empty for the session-less Chat() path) — the rare
// error-recovery retries below fall back to the plain blocking call, since
// they're already a degraded path where perceived-progress polish matters
// far less than keeping the diff (and risk surface) small.
func (e *Engine) chatWithLLM(ctx context.Context, sessionID, message string) (ChatResponse, error) {
	system := e.buildSystemPrompt()
	var buf strings.Builder
	sent := 0
	raw, err := e.llmClient.ChatStream(ctx, system, message, func(chunk string) {
		buf.WriteString(chunk)
		if preview := extractMessagePreview(buf.String()); len(preview) > sent {
			e.publishStream(sessionID, "", preview[sent:], false)
			sent = len(preview)
		}
	})
	e.publishStream(sessionID, "", "", true)
	if err != nil {
		return ChatResponse{}, err
	}

	if resp, ok := parseStructuredResponse(raw); ok {
		// Intent rescue: model returned explain with empty proposals for an
		// actionable request — remind it to populate proposals[].
		if resp.Intent == "explain" && len(resp.Proposals) == 0 && looksActionable(message) {
			rescueMsg := message + "\n\nREMINDER: The user is asking for a specific command. " +
				"You MUST use intent \"suggest_command\" and populate the proposals array. " +
				"Respond only with the JSON object."
			if raw2, err2 := e.llmClient.Chat(ctx, system, rescueMsg); err2 == nil {
				if resp2, ok2 := parseStructuredResponse(raw2); ok2 && len(resp2.Proposals) > 0 {
					slog.Debug("conductor: intent rescue succeeded", "original_intent", resp.Intent)
					return e.validator.ValidateChatResponse(resp2), nil
				}
			}
		}
		return e.validator.ValidateChatResponse(resp), nil
	}

	// Retry: ask the model to produce valid JSON.
	retryMsg := message + "\n\nIMPORTANT: Your previous response was not valid JSON. Reply ONLY with the JSON object."
	if raw2, err2 := e.llmClient.Chat(ctx, system, retryMsg); err2 == nil {
		if resp, ok := parseStructuredResponse(raw2); ok {
			return e.validator.ValidateChatResponse(resp), nil
		}
	}

	// Final fallback: wrap plain-text reply.
	return e.validator.ValidateChatResponse(ChatResponse{
		Intent:     "llm_response",
		Message:    raw,
		Confidence: 0.6,
		References: []string{"llm:" + e.llmClient.Model()},
	}), nil
}

// looksActionable returns true when the message is likely a command request.
// Uses a minimal negative-signal list to avoid false non-rescues; greetings
// and meta-questions are the only exclusions.
func looksActionable(msg string) bool {
	if strings.TrimSpace(msg) == "" {
		return false
	}
	lower := strings.ToLower(msg)
	nonActionable := []string{
		"merhaba", "selam", "nasılsın", "naber", "iyi misin",
		"hello", "hi ", "hey ", "how are you", "what are you", "who are you",
	}
	for _, na := range nonActionable {
		if strings.Contains(lower, na) {
			return false
		}
	}
	return true
}

// extractMessagePreview returns the best-effort, escape-decoded prefix of
// the JSON "message" field's string value from buf — the raw text
// accumulated so far from the model's structured
// {"intent":...,"message":"...",...} chat response (see
// parseStructuredResponse). The model streams the WHOLE JSON object token by
// token, not just prose: without this, publishStream's live "typing"
// preview showed the raw JSON syntax (field names, braces, quotes) as the
// object was built, self-correcting only once the final REST response
// replaced it with the parsed message — a visible flash of JSON before the
// real reply. Best-effort and forgiving: returns "" until the "message" key
// and its opening quote have both arrived, and stops at an escape sequence
// that hasn't fully arrived yet rather than guessing, so the next call （with
// more of the buffer) picks up cleanly. \uXXXX escapes are not decoded
// (models emit raw UTF-8 for non-ASCII in practice) and pass through
// literally rather than risk misparsing a partial one.
func extractMessagePreview(buf string) string {
	const key = `"message"`
	idx := strings.Index(buf, key)
	if idx < 0 {
		return ""
	}
	rest := strings.TrimLeft(buf[idx+len(key):], " \t\r\n")
	rest, ok := strings.CutPrefix(rest, ":")
	if !ok {
		return ""
	}
	rest, ok = strings.CutPrefix(strings.TrimLeft(rest, " \t\r\n"), `"`)
	if !ok {
		return ""
	}
	var out strings.Builder
	for i := 0; i < len(rest); i++ {
		switch c := rest[i]; c {
		case '\\':
			if i+1 >= len(rest) {
				return out.String() // escape not fully arrived yet
			}
			switch rest[i+1] {
			case 'n':
				out.WriteByte('\n')
			case 't':
				out.WriteByte('\t')
			case 'r':
				out.WriteByte('\r')
			default: // '"', '\\', '/', or anything else — pass the escaped byte through
				out.WriteByte(rest[i+1])
			}
			i++
		case '"':
			return out.String() // closing quote — end of the JSON string value
		default:
			out.WriteByte(c)
		}
	}
	return out.String()
}

// parseStructuredResponse tries to unmarshal a ChatResponse from raw text.
func parseStructuredResponse(raw string) (ChatResponse, bool) {
	var resp ChatResponse
	clean := sanitizeVisibleModelText(raw)
	if err := json.Unmarshal([]byte(clean), &resp); err == nil && resp.Intent != "" {
		return resp, true
	}
	// Strip markdown code fences if present.
	trimmed := strings.TrimSpace(clean)
	if idx := strings.Index(trimmed, "{"); idx > 0 {
		trimmed = trimmed[idx:]
	}
	if idx := strings.LastIndex(trimmed, "}"); idx >= 0 && idx < len(trimmed)-1 {
		trimmed = trimmed[:idx+1]
	}
	if err := json.Unmarshal([]byte(trimmed), &resp); err == nil && resp.Intent != "" {
		return resp, true
	}
	return ChatResponse{}, false
}

// buildSystemPrompt composes the runtime system prompt from persona files.
func (e *Engine) buildSystemPrompt() string {
	identity := e.persona.Identity
	if identity == "" {
		identity = "Yummi"
	}

	methodology := ""
	for i, step := range e.persona.Methodology {
		if step != "" {
			methodology += fmt.Sprintf("%d. %s\n", i+1, step)
		}
	}

	boundaries := ""
	for _, b := range e.persona.Boundaries {
		if b != "" {
			boundaries += "- " + b + "\n"
		}
	}

	// Role/disposition come from core_identity.yaml's character block — behavioral
	// guidance the persona file declares but that has no other home in this prompt.
	// Without this, that YAML content is silently inert: present in the file,
	// never seen by the model.
	disposition := ""
	if e.persona.Role != "" {
		disposition += e.persona.Role + "\n"
	}
	for _, d := range []string{
		e.persona.Disposition.OperatorRelationship,
		e.persona.Disposition.Honesty,
		e.persona.Disposition.Economy,
	} {
		if d != "" {
			disposition += "- " + d + "\n"
		}
	}

	// External egress is governed by an operator-set gateway mode (block / allow /
	// ask) that the model cannot see and must not try to infer. The model proposes
	// targets — including hostnames like google.com — freely; the gateway decides
	// reachability at execution time. A block is an operational state to relay
	// plainly, never described as the assistant "lacking permission".
	//
	// hostnamePrecedence is a separate, hard architectural fact (gateway/src/
	// policy/hostname.rs's verdict precedence), not operator-tunable behavior,
	// so it lives here as a hardcoded fact rather than in safety_boundaries.yaml.
	// A live E2E run found the model stating this backwards with high confidence
	// (claiming egress mode "allow" overrides a configured hostname block) —
	// dangerous operator-facing guidance, since the true precedence is the
	// opposite: a hostname_block match is a hard deny that wins regardless of
	// egress mode.
	hostnamePrecedence := "A separate, name-level hostname policy (hostname_allow/hostname_block) can ALSO gate a target by name before any IP-level egress mode is even consulted. Precedence is fixed and does not depend on egress mode: a hostname_block match is a hard deny that wins even when egress mode is \"allow\" — egress mode never overrides a hostname block. Never state that allow mode overrides a hostname-level block; if unsure which one applies, say so rather than guessing.\n"
	scopeSection := "\nNETWORK EGRESS: whether external targets are reachable is decided by the gateway according to an operator-set mode you cannot see (block / allow / ask). Propose any target the operator asks for — including public hostnames like google.com — without pre-judging or refusing on scope grounds. If the gateway blocks or queues a target for approval, report that result factually (e.g. \"gateway blocked this target\" / \"awaiting operator approval\"); do NOT say you personally lack permission or that external access is forbidden in general.\n" + hostnamePrecedence

	// languageRule is rule #3 below. Prefer the persona's own wording (explicitly
	// bidirectional, "no exceptions") over the hardcoded fallback, which only
	// spelled out the Turkish-input direction and left the English direction
	// implicit — live testing showed the model defaulting to Turkish for short
	// English messages early in a conversation, which an asymmetric example
	// plausibly reinforces.
	languageRule := e.persona.Disposition.LanguageParity
	if languageRule == "" {
		languageRule = "Match the operator's language exactly: Turkish input gets Turkish output, English input gets English output. No exceptions."
	}

	prompt := fmt.Sprintf(`You are %s, an elite Red Team penetration testing AI assistant.
%s
EXECUTION ENVIRONMENT:
- You control a Kali Linux container named "sandbox" (Docker, isolated network, IP 172.20.0.10).
- Every command you propose runs INSIDE sandbox via docker exec — not on the operator's machine.
- "local system", "sistemdeki", "bu sistemde" = the sandbox container itself. No IP needed.
- External network access is mediated by the gateway trust boundary; you do not decide what is reachable — propose the target and let the gateway rule on it.
- The operator interacts through the Yummi UI; they read your proposals and approve execution.
%s
CRITICAL RULES:
1. Output ONLY valid JSON. No markdown, no text outside JSON.
2. Commands go in the "proposals" array. Pure answers use "message" only.
3. %s
4. Use your knowledge for commands — never invent flags you are unsure about.
5. Classify risk: low (passive/read-only), medium (generates logs), high (triggers alerts), critical (destructive/irreversible).
6. Do NOT ask for scope/IP when the request clearly refers to the local sandbox container (e.g. "list users", "check disk", "show processes").
7. If the operator's request asks for more than one distinct action (numbered, or joined with "and"/"ve"/commas), task_goal MUST preserve every one of them as its own numbered item — never collapse a multi-part request into one vague sentence. The agent that receives task_goal only sees this field, never the operator's original words, so anything you drop here is gone for good and the agent will silently skip whatever part you summarized away.
`, identity, disposition, scopeSection, languageRule)

	if methodology != "" {
		prompt += "\nWorkflow:\n" + methodology
	}
	if boundaries != "" {
		prompt += "\nConstraints:\n" + boundaries
	}

	prompt += `
OUTPUT FORMAT:

Commands: {"intent":"suggest_command","message":"<brief explanation>","proposals":[{"id":"p1","command":"<exact command>","rationale":"<why>","risk_level":"low|medium|high|critical","requires_approval":false,"target_tool":"<toolname>"}],"confidence":0.9}
Multi-step task: {"intent":"start_task","message":"<one-line plan summary>","task_goal":"<the goal restated clearly — a single request is one sentence, but a request with multiple distinct parts MUST be numbered: 1) ... 2) ... 3) ..., one item per distinct action, none dropped>","confidence":0.9}
Answer:   {"intent":"explain","message":"<answer>","confidence":0.8}
Clarify:  {"intent":"ask_clarification","message":"<question>","confidence":0.5}
Decline:  {"intent":"decline","message":"<reason>","confidence":1.0}

Use "start_task" when the request needs MULTIPLE commands to complete (e.g.
"enumerate this host", "assess the web server"). Use "suggest_command" for
single-command requests.
Start with { and end with }. Nothing else.`

	return prompt
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func (e *Engine) finalize(resp ChatResponse) ChatResponse {
	if e == nil || e.validator == nil {
		return resp
	}
	return e.validator.ValidateChatResponse(resp)
}

func resolvePersonaDir(input string) string {
	if input != "" {
		return input
	}
	candidates := []string{
		"persona/v0.1",
		"../persona/v0.1",
	}
	for _, c := range candidates {
		if stat, err := os.Stat(c); err == nil && stat.IsDir() {
			return c
		}
	}
	return filepath.Join("..", "persona", "v0.1")
}

func localize(input, tr, en string) string {
	if likelyTurkish(input) {
		return tr
	}
	return en
}

func likelyTurkish(s string) bool {
	lower := strings.ToLower(s)
	if strings.ContainsAny(lower, "çğıöşü") {
		return true
	}
	keys := []string{"lütfen", "hedef", "tarama", "tara", "için", "ve", "nasıl"}
	for _, k := range keys {
		if strings.Contains(lower, k) {
			return true
		}
	}
	return false
}

// maxObservationBytes caps how much of an execution output is injected back
// into the LLM history to protect the context window.
const maxObservationBytes = 2000

// AppendTaskOutcome makes a terminal task's server-recorded summary available
// to later ordinary chat turns in the same session. The task ID marker keeps
// the entry idempotent while it remains inside the bounded history window.
func (e *Engine) AppendTaskOutcome(sessionID, taskID, summary string, findings []string) {
	if e == nil || sessionID == "" || taskID == "" {
		return
	}
	if strings.TrimSpace(summary) == "" && len(findings) == 0 {
		return
	}
	marker := "[SERVER TASK OUTCOME id=" + taskID + "]"

	e.histMu.Lock()
	defer e.histMu.Unlock()
	for i, msg := range e.history[sessionID] {
		if strings.Contains(msg.Content, marker) {
			// Refresh the outcome at the tail so a long conversation cannot
			// evict it on the same turn that asked about the completed task.
			history := e.history[sessionID]
			copy(history[i:], history[i+1:])
			history = history[:len(history)-1]
			e.history[sessionID] = capHistory(append(history, msg))
			return
		}
	}

	var b strings.Builder
	b.WriteString(marker)
	b.WriteString("\nThis is the terminal task state recorded by Yummi. Cite the listed evidence; do not invent commands or outputs.\n")
	if strings.TrimSpace(summary) != "" {
		b.WriteString("Summary: ")
		b.WriteString(strings.TrimSpace(summary))
		b.WriteString("\n")
	}
	if len(findings) > 0 {
		b.WriteString("Findings:\n")
		for _, finding := range findings {
			if strings.TrimSpace(finding) != "" {
				b.WriteString("- ")
				b.WriteString(strings.TrimSpace(finding))
				b.WriteString("\n")
			}
		}
	}
	e.history[sessionID] = capHistory(append(e.history[sessionID], llm.Message{Role: "system", Content: b.String()}))
}

// AppendObservation injects an execution result into the session's conversation
// history as a user-role message so the LLM sees the tool output on its next
// turn — closing the agentic loop: propose → approve → run → observe → propose.
//
// Safe to call when e is nil, sessionID is empty, or LLM is disabled.
func (e *Engine) AppendObservation(sessionID, command, output, execErr string) {
	if e == nil || sessionID == "" {
		return
	}
	if command == "" && output == "" && execErr == "" {
		return
	}

	if len(output) > maxObservationBytes {
		output = "...[truncated, showing last " +
			fmt.Sprintf("%d", maxObservationBytes) + " bytes]...\n" +
			output[len(output)-maxObservationBytes:]
	}

	var b strings.Builder
	b.WriteString("[TOOL EXECUTION RESULT]\n")
	if command != "" {
		b.WriteString("Command: ")
		b.WriteString(command)
		b.WriteString("\n")
	}
	if execErr != "" {
		b.WriteString("Error: ")
		b.WriteString(execErr)
		b.WriteString("\n")
	}
	if output != "" {
		b.WriteString("Output:\n")
		b.WriteString(output)
		if !strings.HasSuffix(output, "\n") {
			b.WriteString("\n")
		}
	}
	b.WriteString("[END RESULT]\n\nBased on this output, suggest the next step or summarize findings if the goal is achieved.")

	e.histMu.Lock()
	e.history[sessionID] = capHistory(append(e.history[sessionID], llm.Message{Role: "user", Content: b.String()}))
	e.histMu.Unlock()
}
