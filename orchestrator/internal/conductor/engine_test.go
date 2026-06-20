package conductor

import (
	"context"
	"strings"
	"testing"

	"github.com/n0connect/yummi/orchestrator/internal/llm"
)

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

// TestEngineNoLLMDeclines verifies that without an LLM client the engine
// returns a decline response rather than a fallback rule-based answer.
func TestEngineNoLLMDeclines(t *testing.T) {
	e := &Engine{
		persona:   Persona{},
		validator: NewValidatorFromEnv(),
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
		validator: NewValidatorFromEnv(),
	}

	resp := e.Chat(context.Background(), "")

	if resp.Intent != "ask_clarification" {
		t.Errorf("expected ask_clarification for empty message, got %q", resp.Intent)
	}
}

func TestEngineEmptyMessageWithHistory(t *testing.T) {
	e := &Engine{
		persona:   Persona{},
		validator: NewValidatorFromEnv(),
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

// TestEngineFinalizeAddsRiskWarning ensures the validator attaches a
// high-severity warning when a proposal is tagged high or critical risk.
func TestEngineFinalizeAddsRiskWarning(t *testing.T) {
	e := &Engine{
		persona:   Persona{},
		validator: NewValidatorFromEnv(),
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

	// Invalid
	_, ok3 := parseStructuredResponse("not json at all")
	if ok3 {
		t.Error("expected invalid JSON to not parse")
	}
}

func TestSystemPromptIncludesStartTask(t *testing.T) {
	engine, err := NewEngine("../../persona/v0.1", "", "")
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
