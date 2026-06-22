package conductor

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/n0connect/yummi/orchestrator/internal/llm"
)

// Engine coordinates persona-backed assistant decisions via LLM.
// A nil llmClient means Ollama is not configured; Chat returns decline.
type Engine struct {
	persona   Persona
	validator *Validator
	llmClient *llm.Client
	tier      TierConfig

	histMu  sync.Mutex
	history map[string][]llm.Message // keyed by session ID
}

// maxHistoryPerSession caps stored turns to bound memory usage.
const maxHistoryPerSession = 20

// NewEngine constructs an engine from persona files and optional Ollama settings.
// When ollamaURL is empty, the engine declines all requests with a clear message.
func NewEngine(personaDir, ollamaURL, ollamaModel string) (*Engine, error) {
	persona, err := LoadPersona(resolvePersonaDir(personaDir))
	if err != nil {
		slog.Warn("persona load failed; continuing with empty persona",
			"error", err, "persona_dir", personaDir)
		persona = Persona{}
	}

	tier := DefaultTierRegistry().Resolve(ollamaModel)

	var client *llm.Client
	if ollamaURL != "" {
		opts := llm.DefaultQwen3Options()
		opts.NumCtx = tier.NumCtx
		if tier.Thinking {
			t := true
			opts.Think = &t
		}
		client = llm.NewClient(ollamaURL, tier.Model).WithOptions(opts)
	}

	return &Engine{
		persona:   persona,
		validator: NewValidator(),
		llmClient: client,
		tier:      tier,
		history:   make(map[string][]llm.Message),
	}, nil
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

// OllamaModel returns the configured Ollama model tag, or empty when not configured.
func (e *Engine) OllamaModel() string {
	if e == nil {
		return ""
	}
	return e.llmClient.Model()
}

// LLM exposes the underlying client for non-chat callers (agent runner).
// Nil when Ollama is not configured.
func (e *Engine) LLM() *llm.Client {
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
	if text == "" {
		return e.replyAskClarification(localize(text,
			"Neyi yapmak istediğini bir cümleyle yazar mısın?",
			"Could you describe your goal in one sentence?"), 0.2)
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

	resp, err := e.chatWithLLM(ctx, text)
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
	text := strings.TrimSpace(message)
	if text == "" {
		return e.replyAskClarification(localize(text,
			"Neyi yapmak istediğini bir cümleyle yazar mısın?",
			"Could you describe your goal in one sentence?"), 0.2)
	}

	if e.llmClient == nil || sessionID == "" {
		return e.Chat(ctx, text)
	}

	e.histMu.Lock()
	msgs := e.history[sessionID]
	msgs = append(msgs, llm.Message{Role: "user", Content: text})
	if len(msgs) > maxHistoryPerSession {
		msgs = msgs[len(msgs)-maxHistoryPerSession:]
	}
	e.history[sessionID] = msgs
	e.histMu.Unlock()

	resp, err := e.chatWithLLMHistory(ctx, msgs)
	if err != nil {
		slog.Warn("ollama history chat failed", "error", err, "session", sessionID)
		return e.Chat(ctx, text)
	}

	e.histMu.Lock()
	e.history[sessionID] = append(e.history[sessionID], llm.Message{Role: "assistant", Content: resp.Message})
	e.histMu.Unlock()

	return resp
}

// ClearHistory removes the conversation history for a session.
func (e *Engine) ClearHistory(sessionID string) {
	e.histMu.Lock()
	delete(e.history, sessionID)
	e.histMu.Unlock()
}

// ── LLM helpers ──────────────────────────────────────────────────────────────

func (e *Engine) chatWithLLMHistory(ctx context.Context, history []llm.Message) (ChatResponse, error) {
	system := e.buildSystemPrompt()
	raw, err := e.llmClient.ChatHistory(ctx, system, history)
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
// for an actionable request.
func (e *Engine) chatWithLLM(ctx context.Context, message string) (ChatResponse, error) {
	system := e.buildSystemPrompt()
	raw, err := e.llmClient.Chat(ctx, system, message)
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

// parseStructuredResponse tries to unmarshal a ChatResponse from raw text.
func parseStructuredResponse(raw string) (ChatResponse, bool) {
	var resp ChatResponse
	if err := json.Unmarshal([]byte(raw), &resp); err == nil && resp.Intent != "" {
		return resp, true
	}
	// Strip markdown code fences if present.
	trimmed := strings.TrimSpace(raw)
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

	// Network scope is enforced transparently by the gateway — the orchestrator
	// no longer reasons about a configured allow/block list. The model proposes
	// targets freely; gateway rejects out-of-scope ones at execution time.
	scopeSection := "\nNETWORK SCOPE: enforced transparently by the gateway. Propose targets freely — if one is out of scope, you'll be told at execution time. Do not try to pre-judge reachability yourself.\n"

	prompt := fmt.Sprintf(`You are %s, an elite Red Team penetration testing AI assistant.

EXECUTION ENVIRONMENT:
- You control a Kali Linux container named "sandbox" (Docker, isolated network, IP 172.20.0.10).
- Every command you propose runs INSIDE sandbox via docker exec — not on the operator's machine.
- "local system", "sistemdeki", "bu sistemde" = the sandbox container itself. No IP needed.
- External network access is gated by the gateway trust boundary; only scoped targets can be reached.
- The operator interacts through the Yummi UI; they read your proposals and approve execution.
%s
CRITICAL RULES:
1. Output ONLY valid JSON. No markdown, no text outside JSON.
2. Commands go in the "proposals" array. Pure answers use "message" only.
3. Match the operator's language exactly (Turkish input → Turkish output).
4. Use your knowledge for commands — never invent flags you are unsure about.
5. Classify risk: low (passive/read-only), medium (generates logs), high (triggers alerts), critical (destructive/irreversible).
6. Do NOT ask for scope/IP when the request clearly refers to the local sandbox container (e.g. "list users", "check disk", "show processes").
`, identity, scopeSection)

	if methodology != "" {
		prompt += "\nWorkflow:\n" + methodology
	}
	if boundaries != "" {
		prompt += "\nConstraints:\n" + boundaries
	}

	prompt += `
OUTPUT FORMAT:

Commands: {"intent":"suggest_command","message":"<brief explanation>","proposals":[{"id":"p1","command":"<exact command>","rationale":"<why>","risk_level":"low|medium|high|critical","requires_approval":false,"target_tool":"<toolname>"}],"confidence":0.9}
Multi-step task: {"intent":"start_task","message":"<one-line plan summary>","task_goal":"<the goal restated as one clear sentence>","confidence":0.9}
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

func (e *Engine) replyAskClarification(message string, confidence float64) ChatResponse {
	return e.finalize(ChatResponse{
		Intent:     "ask_clarification",
		Message:    message,
		Confidence: confidence,
		References: []string{"persona:core_identity"},
	})
}

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
	msgs := e.history[sessionID]
	msgs = append(msgs, llm.Message{Role: "user", Content: b.String()})
	if len(msgs) > maxHistoryPerSession {
		msgs = msgs[len(msgs)-maxHistoryPerSession:]
	}
	e.history[sessionID] = msgs
	e.histMu.Unlock()
}
