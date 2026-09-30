package conductor

import (
	"context"
	"strings"
	"testing"

	"github.com/n0connect/yummi/orchestrator/internal/llm"
)

// TestSeedHistoryRestoresButDoesNotClobber verifies the durable-chat-history
// restore path: SeedHistory fills an empty session's in-memory history (so a
// restart restores context) but never overwrites a session that already has
// live turns, and caps to the most recent maxHistoryPerSession.
func TestSeedHistoryRestoresButDoesNotClobber(t *testing.T) {
	// A non-empty Ollama URL gives a non-nil llmClient (lazy, no connection),
	// which SeedHistory requires before it does anything.
	e, err := NewEngine("", "http://127.0.0.1:11434", "qwen3:8b")
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	// Empty session → seeded.
	e.SeedHistory("s1", []llm.Message{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "answer"},
	})
	if got := len(e.history["s1"]); got != 2 {
		t.Fatalf("expected 2 seeded turns, got %d", got)
	}

	// Second seed on a now-populated session is a no-op (no clobber).
	e.SeedHistory("s1", []llm.Message{{Role: "user", Content: "SHOULD NOT APPEAR"}})
	if got := len(e.history["s1"]); got != 2 {
		t.Fatalf("seed clobbered live history: now %d turns", got)
	}
	if e.history["s1"][0].Content != "first" {
		t.Fatalf("live history mutated: %+v", e.history["s1"])
	}

	// Over-cap seed keeps only the tail.
	big := make([]llm.Message, maxHistoryPerSession+10)
	for i := range big {
		big[i] = llm.Message{Role: "user", Content: "m"}
	}
	e.SeedHistory("s2", big)
	if got := len(e.history["s2"]); got != maxHistoryPerSession {
		t.Fatalf("seed not capped: got %d, want %d", got, maxHistoryPerSession)
	}
}

func TestCapHistoryRetainsFiftyMessageConversation(t *testing.T) {
	msgs := make([]llm.Message, 50)
	for i := range msgs {
		msgs[i] = llm.Message{Role: "user", Content: "turn"}
	}
	if got := len(capHistory(msgs)); got != 50 {
		t.Fatalf("50-message conversation was truncated to %d messages", got)
	}
}

// TestAppendObservationFeedsHistory verifies that AppendObservation injects a
// well-formed tool-result message into the session's chat history.
func TestAppendObservationFeedsHistory(t *testing.T) {
	e := &Engine{
		persona: Persona{},
		history: map[string][]llm.Message{},
	}

	e.AppendObservation("s1", "subfinder -d example.com", "found 12 subdomains:\nfoo.example.com\n", "")

	msgs := e.history["s1"]
	if len(msgs) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Errorf("expected role user, got %q", msgs[0].Role)
	}
	for _, want := range []string{
		"[TOOL EXECUTION RESULT]",
		"Command: subfinder -d example.com",
		"foo.example.com",
		"[END RESULT]",
	} {
		if !strings.Contains(msgs[0].Content, want) {
			t.Errorf("observation missing %q\n--- got ---\n%s", want, msgs[0].Content)
		}
	}
}

// TestAppendObservationTruncatesLargeOutput protects the LLM context window
// against multi-MB scan dumps.
func TestAppendObservationTruncatesLargeOutput(t *testing.T) {
	e := &Engine{history: map[string][]llm.Message{}}
	big := strings.Repeat("x", maxObservationBytes*3)
	e.AppendObservation("s1", "noisy-tool", big, "")

	msg := e.history["s1"][0].Content
	if !strings.Contains(msg, "truncated") {
		t.Error("expected truncation marker in observation")
	}
	if len(msg) > maxObservationBytes*2 {
		t.Errorf("truncated message too large: %d bytes", len(msg))
	}
}

// TestAppendObservationNoOps proves nil engine, empty session, and empty
// payload all silently no-op.
func TestAppendObservationNoOps(t *testing.T) {
	var nilEngine *Engine
	nilEngine.AppendObservation("s1", "cmd", "out", "") // must not panic

	e := &Engine{history: map[string][]llm.Message{}}
	e.AppendObservation("", "cmd", "out", "")
	e.AppendObservation("s1", "", "", "")
	if len(e.history) != 0 {
		t.Errorf("expected empty history, got %v", e.history)
	}
}

// TestFeedbackTurnBypassesOperatorBoundaryAndEntersHistoryOnce reproduces the
// live UI failure where a getent result containing the local user "pentester"
// was intercepted as a targetless pentest request before reaching the model.
func TestFeedbackTurnBypassesOperatorBoundaryAndEntersHistoryOnce(t *testing.T) {
	srv := newNDJSONOllamaServer(t, `{"intent":"explain","message":"pentester is a local account","confidence":0.9}`)
	e, err := NewEngine("", srv.URL, "test-model")
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	feedback := "[COMMAND DONE] getent passwd\nOutput:\npentester:x:1000:1000::/home/pentester:/bin/bash"
	resp, err := e.ChatWithHistoryFeedbackTurn(context.Background(), "s1", "turn-feedback", feedback)
	if err != nil {
		t.Fatalf("feedback turn: %v", err)
	}
	if resp.Intent != "explain" || resp.Message != "pentester is a local account" {
		t.Fatalf("feedback was intercepted before the model: %+v", resp)
	}

	userEntries := 0
	for _, msg := range e.history["s1"] {
		if msg.Role == "user" && msg.Content == feedback {
			userEntries++
		}
	}
	if userEntries != 1 {
		t.Fatalf("feedback history entries = %d, want exactly 1: %+v", userEntries, e.history["s1"])
	}
}

func TestAppendTaskOutcomeAddsOneServerContextEntry(t *testing.T) {
	e := &Engine{history: map[string][]llm.Message{}}
	findings := []string{"Hostname: sandbox-1 (from hostname)", "Python: 3.14.6 (from python3 --version)"}

	e.AppendTaskOutcome("s1", "task-1", "Collected verified system metadata.", findings)
	e.AppendTaskOutcome("s1", "task-1", "Collected verified system metadata.", findings)

	msgs := e.history["s1"]
	if len(msgs) != 1 {
		t.Fatalf("task outcome entries = %d, want exactly 1: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "system" {
		t.Fatalf("task outcome role = %q, want system", msgs[0].Role)
	}
	for _, want := range []string{"task-1", "Collected verified system metadata.", "Hostname: sandbox-1", "Python: 3.14.6"} {
		if !strings.Contains(msgs[0].Content, want) {
			t.Errorf("task outcome missing %q: %s", want, msgs[0].Content)
		}
	}

	e.history["s1"] = append(e.history["s1"], llm.Message{Role: "user", Content: "later turn"})
	e.AppendTaskOutcome("s1", "task-1", "Collected verified system metadata.", findings)
	msgs = e.history["s1"]
	if len(msgs) != 2 || msgs[len(msgs)-1].Role != "system" {
		t.Fatalf("task outcome was not refreshed at the history tail: %+v", msgs)
	}
}

// TestEngineNoLLMDeclines verifies that without an LLM client the engine
// returns a decline response rather than a fallback rule-based answer.
func TestEngineNoLLMDeclines(t *testing.T) {
	e := &Engine{
		persona:   Persona{},
		validator: NewValidator(),
		// llmClient is nil — no LLM configured
	}

	resp := e.Chat(context.Background(), "scan example.com")

	if resp.Intent != "decline" {
		t.Errorf("expected decline when LLM not configured, got %q", resp.Intent)
	}
	lower := strings.ToLower(resp.Message)
	if !strings.Contains(lower, "ollama") && !strings.Contains(lower, "llm") {
		t.Errorf("decline message should mention ollama/llm, got: %s", resp.Message)
	}
}

func TestEngineEmptyMessage(t *testing.T) {
	e := &Engine{
		persona:   Persona{},
		validator: NewValidator(),
	}

	resp := e.Chat(context.Background(), "")

	if resp.Intent != "ask_clarification" {
		t.Errorf("expected ask_clarification for empty message, got %q", resp.Intent)
	}
}

func TestEngineEmptyMessageWithHistory(t *testing.T) {
	e := &Engine{
		persona:   Persona{},
		validator: NewValidator(),
		history:   make(map[string][]llm.Message),
	}

	resp := e.ChatWithHistory(context.Background(), "sess-1", "")

	if resp.Intent != "ask_clarification" {
		t.Errorf("expected ask_clarification for empty message, got %q", resp.Intent)
	}
}

func TestEngineWithOllamaDisabled(t *testing.T) {
	e, err := NewEngine("", "", "")
	if err != nil {
		t.Fatalf("NewEngine with empty config must not error, got %v", err)
	}
	if e == nil {
		t.Fatal("expected engine to be non-nil")
	}
	if e.llmClient != nil {
		t.Error("expected llmClient to be nil when ollamaURL is empty")
	}
	resp := e.Chat(context.Background(), "scan example.com")
	if resp.Intent != "decline" {
		t.Errorf("expected decline without LLM, got intent %q", resp.Intent)
	}
}

func TestEngineOllamaModelReporting(t *testing.T) {
	e, err := NewEngine("", "http://127.0.0.1:11434", "qwen3:14b")
	if err != nil {
		t.Fatalf("NewEngine returned error: %v", err)
	}
	if got := e.OllamaModel(); got != "qwen3:14b" {
		t.Errorf("expected OllamaModel to report qwen3:14b, got %q", got)
	}

	empty, err := NewEngine("", "", "")
	if err != nil {
		t.Fatalf("NewEngine returned error: %v", err)
	}
	if got := empty.OllamaModel(); got != "" {
		t.Errorf("expected empty OllamaModel when disabled, got %q", got)
	}
}

// TestNewEngineWithOverrides_CustomModelGetsPlainNewEngineDefaults proves
// NewEngine (used by every existing caller) is unaffected by adding the
// overrides mechanism — a custom model tag still resolves to Standard's fixed
// 32K-context, no-thinking profile when no overrides are given.
func TestNewEngineWithOverrides_CustomModelGetsPlainNewEngineDefaults(t *testing.T) {
	e, err := NewEngine("", "http://127.0.0.1:11434", "kimi-k2:latest")
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	tier := e.Tier()
	if tier.Model != "kimi-k2:latest" {
		t.Fatalf("Model = %q, want the custom tag preserved", tier.Model)
	}
	if tier.NumCtx != 32768 {
		t.Fatalf("NumCtx = %d, want Standard's default 32768 with no override", tier.NumCtx)
	}
	if tier.Thinking {
		t.Fatal("Thinking = true, want Standard's default false with no override")
	}
}

// TestNewEngineWithOverrides_CustomModelHonorsExplicitOverrides is the
// actual point of TierOverrides: an operator plugging in a large-context
// custom model (e.g. someone with the hardware for Kimi K2 or similar) isn't
// stuck at Standard's fixed 32K ceiling — YUMMI_OLLAMA_NUM_CTX/_THINKING flow
// through config.Config into exactly this override path (see main.go).
func TestNewEngineWithOverrides_CustomModelHonorsExplicitOverrides(t *testing.T) {
	thinking := true
	e, err := NewEngineWithOverrides("", "http://127.0.0.1:11434", "kimi-k2:latest", TierOverrides{
		NumCtx: 131072, Thinking: &thinking,
	})
	if err != nil {
		t.Fatalf("NewEngineWithOverrides: %v", err)
	}
	tier := e.Tier()
	if tier.NumCtx != 131072 {
		t.Fatalf("NumCtx = %d, want the overridden 131072", tier.NumCtx)
	}
	if !tier.Thinking {
		t.Fatal("Thinking = false, want the overridden true")
	}
}

// TestNewEngineWithOverrides_CloudBaseURLReplacesOllama is the actual point
// of CloudBaseURL: an operator who has pointed an agent backend at a cloud
// model gets conductor moved to that SAME model too, not left on Ollama
// alongside it — see ChatBackend's doc comment on why this is a mandatory
// replacement, not an addition. Model() must report the cloud tag, not the
// Ollama tier's resolved model, proving the swap actually happened rather
// than just being ignored.
func TestNewEngineWithOverrides_CloudBaseURLReplacesOllama(t *testing.T) {
	e, err := NewEngineWithOverrides("", "http://127.0.0.1:11434", "gpt-oss:20b", TierOverrides{
		CloudBaseURL: "https://openrouter.ai/api/v1",
		CloudModel:   "z-ai/glm-5.2:free",
		CloudAPIKey:  "sk-or-test",
	})
	if err != nil {
		t.Fatalf("NewEngineWithOverrides: %v", err)
	}
	backend := e.LLM()
	if backend == nil {
		t.Fatal("LLM() = nil, want a configured cloud backend")
	}
	if got := backend.Model(); got != "z-ai/glm-5.2:free" {
		t.Fatalf("Model() = %q, want the cloud model — Ollama's gpt-oss:20b must not still be active", got)
	}
	if _, ok := backend.(*llm.OpenAIClient); !ok {
		t.Fatalf("LLM() backend is %T, want *llm.OpenAIClient", backend)
	}
}

// TestEngine_Backend_ReportsActualProtocol guards the exact regression found
// live: api/system.go's health status hardcoded "backend": "ollama" whenever
// any model was configured, even after CloudBaseURL had replaced it with
// OpenRouter — meta.model correctly showed the cloud tag while meta.backend
// kept lying. Backend() must distinguish all three real states.
func TestEngine_Backend_ReportsActualProtocol(t *testing.T) {
	none, err := NewEngineWithOverrides("", "", "", TierOverrides{})
	if err != nil {
		t.Fatalf("NewEngineWithOverrides (none): %v", err)
	}
	if got := none.Backend(); got != "none" {
		t.Errorf("Backend() = %q, want %q for an unconfigured engine", got, "none")
	}

	ollama, err := NewEngineWithOverrides("", "http://127.0.0.1:11434", "gpt-oss:20b", TierOverrides{})
	if err != nil {
		t.Fatalf("NewEngineWithOverrides (ollama): %v", err)
	}
	if got := ollama.Backend(); got != "ollama" {
		t.Errorf("Backend() = %q, want %q for a plain Ollama config", got, "ollama")
	}

	cloud, err := NewEngineWithOverrides("", "http://127.0.0.1:11434", "gpt-oss:20b", TierOverrides{
		CloudBaseURL: "https://openrouter.ai/api/v1",
		CloudModel:   "z-ai/glm-5.2:free",
		CloudAPIKey:  "sk-or-test",
	})
	if err != nil {
		t.Fatalf("NewEngineWithOverrides (cloud): %v", err)
	}
	if got := cloud.Backend(); got != "cloud" {
		t.Errorf("Backend() = %q, want %q once CloudBaseURL replaces Ollama", got, "cloud")
	}
}

// TestEngine_LLM_NilWhenNothingConfigured guards the exact Go footgun
// NewEngineWithOverrides' own comment calls out: assigning a nil *llm.Client
// into the ChatBackend interface field would produce a NON-nil interface
// (it has a concrete type, just a nil pointer inside), silently breaking
// every "== nil" check downstream. This must stay a genuine nil interface
// when neither an Ollama URL nor a cloud override is configured.
func TestEngine_LLM_NilWhenNothingConfigured(t *testing.T) {
	e, err := NewEngineWithOverrides("", "", "", TierOverrides{})
	if err != nil {
		t.Fatalf("NewEngineWithOverrides: %v", err)
	}
	if e.LLM() != nil {
		t.Fatalf("LLM() = %#v, want a true nil interface with no backend configured", e.LLM())
	}
}

// TestNewEngineWithOverrides_ZeroOverridesLeaveTierAlone proves a named tier
// (not a custom tag) is unaffected by a zero-value TierOverrides — overrides
// are additive, never a silent replacement for the tier system.
func TestNewEngineWithOverrides_ZeroOverridesLeaveTierAlone(t *testing.T) {
	e, err := NewEngineWithOverrides("", "http://127.0.0.1:11434", "expert", TierOverrides{})
	if err != nil {
		t.Fatalf("NewEngineWithOverrides: %v", err)
	}
	if got := e.Tier().NumCtx; got != 32768 {
		t.Fatalf("NumCtx = %d, want expert tier's own 32768", got)
	}
}

// TestEngineFinalizeAddsRiskWarning ensures the validator attaches a
// high-severity warning when a proposal is tagged high or critical risk.
func TestEngineFinalizeAddsRiskWarning(t *testing.T) {
	e := &Engine{
		persona:   Persona{},
		validator: NewValidator(),
	}

	resp := e.finalize(ChatResponse{
		Intent:  "suggest_command",
		Message: "test",
		Proposals: []Proposal{
			{
				ID:         "p_high",
				Command:    "hydra -l admin -P /wordlists/common.txt ssh://10.0.0.1",
				RiskLevel:  "high",
				TargetTool: "hydra",
				ToolArgs:   map[string]any{"target": "10.0.0.1"},
			},
		},
		Confidence: 0.8,
	})

	if len(resp.Warnings) == 0 {
		t.Fatal("expected a warning for high-risk proposal")
	}
	found := false
	for _, w := range resp.Warnings {
		lw := strings.ToLower(w)
		if strings.Contains(lw, "high") && strings.Contains(lw, "approval") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected explicit high-risk approval warning, got %v", resp.Warnings)
	}
	if !resp.Proposals[0].RequiresApproval {
		t.Error("high-risk proposal must require approval after finalize")
	}
}

func TestLocalizeDetection(t *testing.T) {
	trInput := "lütfen hedef için tarama yap"
	enInput := "please scan the target"

	if got := localize(trInput, "TR", "EN"); got != "TR" {
		t.Errorf("expected TR for Turkish input, got %s", got)
	}
	if got := localize(enInput, "TR", "EN"); got != "EN" {
		t.Errorf("expected EN for English input, got %s", got)
	}
}

func TestLooksActionable(t *testing.T) {
	actionable := []string{
		"scan 192.168.1.1",
		"enumerate subdomains of example.com",
		"check open ports",
	}
	nonActionable := []string{
		"hello",
		"hi there",
		"merhaba",
		"nasılsın",
		"",
	}

	for _, msg := range actionable {
		if !looksActionable(msg) {
			t.Errorf("expected %q to be actionable", msg)
		}
	}
	for _, msg := range nonActionable {
		if looksActionable(msg) {
			t.Errorf("expected %q to be non-actionable", msg)
		}
	}
}

func TestParseStructuredResponse(t *testing.T) {
	valid := `{"intent":"suggest_command","message":"test","confidence":0.9}`
	resp, ok := parseStructuredResponse(valid)
	if !ok {
		t.Fatal("expected valid JSON to parse")
	}
	if resp.Intent != "suggest_command" {
		t.Errorf("expected suggest_command, got %q", resp.Intent)
	}

	// With markdown fences
	fenced := "```json\n" + valid + "\n```"
	resp2, ok2 := parseStructuredResponse(fenced)
	if !ok2 {
		t.Fatal("expected fenced JSON to parse")
	}
	if resp2.Intent != "suggest_command" {
		t.Errorf("expected suggest_command from fenced, got %q", resp2.Intent)
	}

	// Hidden reasoning before a structured reply must be removed before JSON
	// parsing, never pushed into the plain-text fallback shown by the UI.
	reasoningWrapped := "<think>private reasoning</think>\n" + valid
	resp3, ok3 := parseStructuredResponse(reasoningWrapped)
	if !ok3 || resp3.Message != "test" {
		t.Fatalf("expected reasoning-wrapped JSON to parse safely, got ok=%v resp=%+v", ok3, resp3)
	}

	// Invalid
	_, ok4 := parseStructuredResponse("not json at all")
	if ok4 {
		t.Error("expected invalid JSON to not parse")
	}
}

// TestExtractMessagePreview_GrowsMonotonicallyAsBufferStreams is the
// regression for the JSON-flash bug: chatWithLLMHistory used to publish
// every raw chunk straight from the model, so the operator's live "typing"
// preview briefly showed literal JSON syntax ({"intent":"...","message":"…)
// before the REST reply replaced it with the parsed message. Simulates the
// buffer growing one character at a time, as it would across many small
// stream chunks, and asserts the extracted preview only ever grows and never
// contains a stray brace/quote from the surrounding JSON structure.
func TestExtractMessagePreview_GrowsMonotonicallyAsBufferStreams(t *testing.T) {
	full := `{"intent":"suggest_command","message":"Hedefi taramak için nmap kullanabiliriz.","proposals":[]}`
	var buf strings.Builder
	prev := ""
	for _, r := range full {
		buf.WriteRune(r)
		preview := extractMessagePreview(buf.String())
		if len(preview) < len(prev) {
			t.Fatalf("preview shrank: prev=%q now=%q", prev, preview)
		}
		if !strings.HasPrefix(preview, prev) {
			t.Fatalf("preview %q is not an extension of prior preview %q", preview, prev)
		}
		if strings.ContainsAny(preview, "{}") {
			t.Fatalf("preview leaked JSON structure: %q", preview)
		}
		prev = preview
	}
	want := "Hedefi taramak için nmap kullanabiliriz."
	if prev != want {
		t.Fatalf("final preview = %q, want %q", prev, want)
	}
}

func TestExtractMessagePreview(t *testing.T) {
	cases := []struct {
		name string
		buf  string
		want string
	}{
		{"no message key yet", `{"intent":"suggest_command"`, ""},
		{"key present, value not started", `{"intent":"x","message"`, ""},
		{"key and colon, no quote yet", `{"intent":"x","message":`, ""},
		{"partial value mid-string", `{"intent":"x","message":"Merha`, "Merha"},
		{"complete value", `{"intent":"x","message":"Merhaba"}`, "Merhaba"},
		{"escaped quote", `{"message":"She said \"hi\""`, `She said "hi"`},
		{"escaped backslash", `{"message":"C:\\path"`, `C:\path`},
		{"escaped newline", `{"message":"line1\nline2"`, "line1\nline2"},
		{"dangling escape stops cleanly", `{"message":"abc\`, "abc"},
		{"turkish characters pass through", `{"message":"İçinde şık çözüm var"`, "İçinde şık çözüm var"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractMessagePreview(tc.buf)
			if got != tc.want {
				t.Errorf("extractMessagePreview(%q) = %q, want %q", tc.buf, got, tc.want)
			}
		})
	}
}

// Regression: the system prompt's only language instruction used to be the
// hardcoded, one-directional "Match the operator's language exactly (Turkish
// input → Turkish output)" — found live to plausibly bias the model toward
// Turkish since the English direction was only implicit. buildSystemPrompt
// must now surface the persona's explicit, bidirectional language_parity
// rule from core_identity.yaml's character.disposition block.
func TestSystemPromptUsesPersonaLanguageParity(t *testing.T) {
	engine, err := NewEngine("../../../persona/v0.1", "", "")
	if err != nil {
		t.Fatalf("engine init: %v", err)
	}
	if engine.persona.Disposition.LanguageParity == "" {
		t.Fatal("persona's language_parity field loaded empty — core_identity.yaml's character block isn't being parsed")
	}
	prompt := engine.buildSystemPrompt()
	if !strings.Contains(prompt, engine.persona.Disposition.LanguageParity) {
		t.Fatalf("system prompt does not include the persona's language_parity text:\n%s", prompt)
	}
	if !strings.Contains(prompt, "English input") {
		t.Fatal("system prompt's language rule does not explicitly cover the English direction")
	}
}

// TestSystemPromptStatesHostnameBlockPrecedenceCorrectly is a live-E2E-found
// regression test: a run against the real model found it stating gateway
// precedence backwards with high confidence (claiming egress mode "allow"
// overrides a configured hostname block, when the actual precedence — see
// gateway/src/policy/hostname.rs — is the opposite: hostname_block always
// wins). This locks the corrected fact into the checked-in prompt.
func TestSystemPromptStatesHostnameBlockPrecedenceCorrectly(t *testing.T) {
	engine, err := NewEngine("../../../persona/v0.1", "", "")
	if err != nil {
		t.Fatalf("engine init: %v", err)
	}
	prompt := engine.buildSystemPrompt()
	if !strings.Contains(prompt, "hostname_block match is a hard deny that wins even when egress mode is \"allow\"") {
		t.Fatalf("system prompt does not state correct hostname/egress-mode precedence:\n%s", prompt)
	}
}

// TestSystemPromptIncludesEvidenceDisciplineBoundaries is a live-E2E-found
// regression test: chat answers (intent: explain) were observed inventing
// command output that never ran (a fabricated hostnamectl result) and
// restating an operator's unverified assumption ("assume Ubuntu 99") as the
// model's own confirmed finding. safety_boundaries.yaml now carries explicit
// rules against both; this locks their presence in the rendered prompt.
func TestSystemPromptIncludesEvidenceDisciplineBoundaries(t *testing.T) {
	engine, err := NewEngine("../../../persona/v0.1", "", "")
	if err != nil {
		t.Fatalf("engine init: %v", err)
	}
	prompt := engine.buildSystemPrompt()
	for _, want := range []string{
		"Never state or imply that a specific command ran",
		"Never echo it back as your own confirmed finding",
		"Never propose, describe, or imply having done the forbidden thing",
		"do not reflexively deny it",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("system prompt missing evidence-discipline boundary %q:\n%s", want, prompt)
		}
	}
}

// TestSystemPromptDistinguishesSuggestedFromExecutedCommands is a live-long-
// form-testing-found regression test: a real 30+ turn conversation showed
// the model repeatedly claiming to have "already seen" the output of
// commands it had only ever suggested (suggest_command proposals the
// operator never approved) — twice, for both a "whoami/id" proposal and a
// later "php -v" proposal it had never actually run, describing invented
// "command output" content with full confidence. Locks the corrected
// suggested-vs-executed distinction into the rendered prompt.
func TestSystemPromptDistinguishesSuggestedFromExecutedCommands(t *testing.T) {
	engine, err := NewEngine("../../../persona/v0.1", "", "")
	if err != nil {
		t.Fatalf("engine init: %v", err)
	}
	prompt := engine.buildSystemPrompt()
	if !strings.Contains(prompt, "A command you SUGGESTED") {
		t.Fatalf("system prompt does not distinguish a suggested command from an executed one:\n%s", prompt)
	}
}

func TestSystemPromptIncludesStartTask(t *testing.T) {
	engine, err := NewEngine("../../../persona/v0.1", "", "")
	if err != nil {
		t.Fatalf("engine init: %v", err)
	}
	prompt := engine.buildSystemPrompt()
	if !strings.Contains(prompt, "start_task") {
		t.Fatal("system prompt missing start_task intent")
	}
	if !strings.Contains(prompt, "task_goal") {
		t.Fatal("system prompt missing task_goal field")
	}
}

// TestSystemPromptPreservesMultiPartTaskGoal is a live-long-form-testing-
// found regression test: task_goal previously told the model to restate the
// operator's request "as one clear sentence" with no exception for a
// multi-part request — live-observed collapsing an explicit 6-item numbered
// operator goal into one vague English sentence before the agent ever saw
// it. Since agent.SplitEnumeratedParts (the deterministic, non-LLM-judgment
// multi-part detector) and every downstream step-budget/finish-coverage
// mechanism only ever see task_goal, never the operator's original words,
// this collapse silently discarded the very structure those mechanisms
// depend on — no fix inside the agent package can recover detail lost this
// far upstream. Proves the prompt now explicitly instructs the model to
// number each distinct part instead of collapsing them.
func TestSystemPromptPreservesMultiPartTaskGoal(t *testing.T) {
	engine, err := NewEngine("../../../persona/v0.1", "", "")
	if err != nil {
		t.Fatalf("engine init: %v", err)
	}
	prompt := engine.buildSystemPrompt()
	if !strings.Contains(prompt, "task_goal MUST preserve every one of them as its own numbered item") {
		t.Fatalf("system prompt does not instruct the model to preserve multi-part structure in task_goal:\n%s", prompt)
	}
}
