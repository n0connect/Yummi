package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/n0connect/yummi/orchestrator/internal/llm"
)

// newFakeOllama starts a real HTTP server that returns the provided responses
// in sequence to simulate Ollama LLM calls. The last response is repeated
// indefinitely once the list is exhausted.
//
// This is NOT a mock — the LLM client sends real HTTP requests over the network
// to a real HTTP server. It tests the full HTTP+JSON protocol path.
func newFakeOllama(t *testing.T, responses ...string) *llm.Client {
	t.Helper()
	var mu sync.Mutex
	idx := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		content := responses[idx]
		if idx < len(responses)-1 {
			idx++
		}
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		// Encode content as a JSON string to escape special chars.
		contentJSON, _ := json.Marshal(content)
		fmt.Fprintf(w, `{"message":{"role":"assistant","content":%s},"done":true}`, contentJSON)
	}))
	t.Cleanup(srv.Close)
	return llm.NewClient(srv.URL, "test-model")
}

// testPersona is a minimal persona fixture for tests (test data, not production
// content — production loads persona/v0.1/agent.yaml).
var testPersona = AgentPersona{
	Identity: "You are a test pentest operator working inside an isolated sandbox.",
	Planning: "Produce a short ordered plan toward the goal.",
}

// fastRetryCfg returns a Config with minimal retry delays for testing. Tests
// default to auto mode (no approval gating); the approval test opts into
// ask_permission explicitly.
func fastRetryCfg() Config {
	return Config{
		StepBudget:      10,
		ApprovalTimeout: 5 * time.Second,
		TokenBudget:     6000,
		ApprovalMode:    ModeAuto,
		Persona:         testPersona,
		Retry: RetryConfig{
			MaxAttempts: 3,
			BaseDelay:   1 * time.Millisecond,
			Multiplier:  2.0,
			MaxDelay:    5 * time.Millisecond,
		},
	}
}

// newRunningTask creates a task already in StatusRunning state with a plan.
func newRunningTask(t *testing.T, store *Store, goal string) Task {
	t.Helper()
	task, err := store.Create("test-session", goal, 10)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := store.Transition(task.ID, StatusPlanned, func(tk *Task) {
		tk.Plan = []PlanStep{
			{ID: "s1", Description: "initial recon step", Status: StepPending},
		}
	}); err != nil {
		t.Fatalf("transition to planned: %v", err)
	}
	if err := store.Transition(task.ID, StatusRunning, nil); err != nil {
		t.Fatalf("transition to running: %v", err)
	}
	task, _ = store.Get(task.ID)
	return task
}

// ── Tests ────────────────────────────────────────────────────────────────────

// TestGraphBudgetExhaustion: when StepsUsed == StepBudget, stepNode immediately
// routes to finishNode (budget exhausted) without calling the LLM or exec.
func TestGraphBudgetExhaustion(t *testing.T) {
	store, err := NewStore(openTestDB(t))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	task, err := store.Create("test-session", "enumerate ports", 5)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.Transition(task.ID, StatusPlanned, func(tk *Task) {
		tk.Plan = []PlanStep{{ID: "s1", Description: "nmap scan", Status: StepPending}}
		tk.StepsUsed = 5 // already at budget
	}); err != nil {
		t.Fatalf("transition planned: %v", err)
	}
	if err := store.Transition(task.ID, StatusRunning, nil); err != nil {
		t.Fatalf("transition running: %v", err)
	}

	s := &GraphState{
		taskID: task.ID,
		store:  store,
		gate:   NewGate(),
		cfg:    fastRetryCfg(),
		// llm and exec are intentionally nil — budget exhaustion must not call them.
	}

	if err := Execute(context.Background(), s, nodeStep); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got, _ := store.Get(task.ID)
	if got.Status != StatusFailed {
		t.Fatalf("expected failed (budget exhausted is a failure), got %s (lastError: %q)", got.Status, got.LastError)
	}
	if !strings.Contains(strings.ToLower(got.LastError), "budget") {
		t.Fatalf("expected 'budget' in lastError, got %q", got.LastError)
	}
}

func TestStepNodeConvergesWhenPlanIsTerminalWithoutCallingLLM(t *testing.T) {
	store, err := NewStore(openTestDB(t))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	task, err := store.Create("terminal-plan", "report hostname", 5)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.Transition(task.ID, StatusPlanned, func(tk *Task) {
		tk.Plan = []PlanStep{{ID: "s1", Description: "hostname", Status: StepDone, Result: "sandbox"}}
		tk.StepsUsed = 1
	}); err != nil {
		t.Fatalf("planned: %v", err)
	}
	if err := store.Transition(task.ID, StatusRunning, nil); err != nil {
		t.Fatalf("running: %v", err)
	}
	s := &GraphState{taskID: task.ID, store: store, cfg: fastRetryCfg(), autoFinishTerminalPlan: true, memCtx: MemoryContext{TokenBudget: 6000, Observations: []string{"sandbox"}}}
	s.llm = newFakeOllama(t, "not a control message")
	edge, err := stepNode(context.Background(), s)
	if err != nil || edge != edgeFinish || s.finishOutcome != outcomeCompleted {
		t.Fatalf("terminal plan did not converge: edge=%q outcome=%q err=%v", edge, s.finishOutcome, err)
	}
}

func TestStepNodeDoesNotConvergeAnAllFailedPlan(t *testing.T) {
	store, err := NewStore(openTestDB(t))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	task, _ := store.Create("failed-plan", "recover hostname", 5)
	_ = store.Transition(task.ID, StatusPlanned, func(tk *Task) {
		tk.Plan = []PlanStep{{ID: "s1", Description: "hostnamectl", Status: StepFailed, Error: "systemd unavailable"}}
		tk.StepsUsed = 1
	})
	_ = store.Transition(task.ID, StatusRunning, nil)
	s := &GraphState{
		taskID: task.ID, store: store, cfg: fastRetryCfg(), autoFinishTerminalPlan: true,
		memCtx: MemoryContext{TokenBudget: 6000, Observations: []string{"systemd unavailable"}},
		llm: newFakeOllama(t, `[RUN_COMMAND]
{"command":"hostname","step_id":"s1","risk_level":"low"}`),
	}
	edge, err := stepNode(context.Background(), s)
	if err != nil || edge != edgeRunCommand {
		t.Fatalf("all-failed plan should be allowed to recover: edge=%q err=%v", edge, err)
	}
}

// TestGraphImmediateFinish: LLM returns finish action on the first call.
// Tests the happy path: step → finish → StatusCompleted.
func TestGraphImmediateFinish(t *testing.T) {
	finishAction := "[FINISH]\n" + `{"summary":"host is up, port 22 open"}`
	ollama := newFakeOllama(t, finishAction)

	store, _ := NewStore(openTestDB(t))
	task := newRunningTask(t, store, "check if host is up")

	s := &GraphState{
		taskID: task.ID,
		store:  store,
		llm:    ollama,
		gate:   NewGate(),
		cfg:    fastRetryCfg(),
	}

	if err := Execute(context.Background(), s, nodeStep); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got, _ := store.Get(task.ID)
	if got.Status != StatusCompleted {
		t.Fatalf("expected completed, got %s (lastError: %q)", got.Status, got.LastError)
	}
	if got.Summary != "host is up, port 22 open" {
		t.Fatalf("summary mismatch: %q", got.Summary)
	}
}

// TestGraphAskOperator: LLM returns ask_operator → askNode → StatusPaused.
func TestGraphAskOperator(t *testing.T) {
	askAction := "[ASK_OPERATOR]\n" + `{"question":"which IP should I scan?"}`
	ollama := newFakeOllama(t, askAction)

	store, _ := NewStore(openTestDB(t))
	task := newRunningTask(t, store, "scan a host")

	s := &GraphState{
		taskID: task.ID,
		store:  store,
		llm:    ollama,
		gate:   NewGate(),
		cfg:    fastRetryCfg(),
	}

	if err := Execute(context.Background(), s, nodeStep); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got, _ := store.Get(task.ID)
	if got.Status != StatusPaused {
		t.Fatalf("expected paused, got %s", got.Status)
	}
	if got.Question != "which IP should I scan?" {
		t.Fatalf("question mismatch: %q", got.Question)
	}
}

// TestGraphRetryExhaustion: LLM always returns HTTP 500.
// After MaxAttempts retries, the graph routes to askNode → StatusPaused.
func TestGraphRetryExhaustion(t *testing.T) {
	// Fake server always responds with a 500 error — the LLM client returns an error.
	errorSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"model unavailable"}`))
	}))
	t.Cleanup(errorSrv.Close)

	store, _ := NewStore(openTestDB(t))
	task := newRunningTask(t, store, "scan target")

	s := &GraphState{
		taskID: task.ID,
		store:  store,
		llm:    llm.NewClient(errorSrv.URL, "test-model"),
		gate:   NewGate(),
		cfg:    fastRetryCfg(), // 1ms retry delays for fast tests
	}

	if err := Execute(context.Background(), s, nodeStep); err != nil {
		t.Fatalf("execute returned hard error: %v", err)
	}

	got, _ := store.Get(task.ID)
	if got.Status != StatusPaused {
		t.Fatalf("expected paused after retry exhaustion, got %s (lastError: %q)", got.Status, got.LastError)
	}
	if got.LastError == "" {
		t.Fatal("expected LastError to be set after retry exhaustion")
	}
}

// TestGraphContextCompressionTriggered: when memCtx is above 80% capacity,
// stepNode routes to summarizeNode. After compression, step resumes normally.
func TestGraphContextCompressionTriggered(t *testing.T) {
	summarizeResp := "Summary: prior scans found host at 10.0.0.5 with ports 22, 80 open."
	finishAction := "[FINISH]\n" + `{"summary":"recon complete after compression"}`
	ollama := newFakeOllama(t, summarizeResp, finishAction)

	store, _ := NewStore(openTestDB(t))
	task := newRunningTask(t, store, "enumerate host")

	s := &GraphState{
		taskID: task.ID,
		store:  store,
		llm:    ollama,
		gate:   NewGate(),
		cfg:    Config{StepBudget: 10, TokenBudget: 1000, Retry: fastRetryCfg().Retry},
		memCtx: MemoryContext{
			TokensUsed:   850, // 85% of 1000 → NeedsCompression() == true
			TokenBudget:  1000,
			Observations: []string{"nmap output line 1", "nmap output line 2"},
		},
	}

	if err := Execute(context.Background(), s, nodeStep); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got, _ := store.Get(task.ID)
	if got.Status != StatusCompleted {
		t.Fatalf("expected completed after compression, got %s (lastError: %q)", got.Status, got.LastError)
	}
	if got.Summary != "recon complete after compression" {
		t.Fatalf("summary mismatch: %q", got.Summary)
	}
	// After compression, token counter must have been reset (then incremented again by finish call)
	if s.memCtx.Summary == "" {
		t.Fatal("expected memCtx.Summary to be set after compression")
	}
}

// TestGraphThinkTokensStripped: LLM response wrapped in <think>...</think> blocks
// must still parse correctly. This tests Qwen3/DeepSeek compatibility.
func TestGraphThinkTokensStripped(t *testing.T) {
	thinkWrapped := `<think>
Let me analyze the task and decide the next action.
I should finish since the goal seems straightforward.
</think>
[FINISH]
{"summary":"think-wrapped finish"}`

	ollama := newFakeOllama(t, thinkWrapped)

	store, _ := NewStore(openTestDB(t))
	task := newRunningTask(t, store, "quick recon")

	s := &GraphState{
		taskID: task.ID,
		store:  store,
		llm:    ollama,
		gate:   NewGate(),
		cfg:    fastRetryCfg(),
	}

	if err := Execute(context.Background(), s, nodeStep); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got, _ := store.Get(task.ID)
	if got.Status != StatusCompleted {
		t.Fatalf("expected completed, got %s (lastError: %q)", got.Status, got.LastError)
	}
	if got.Summary != "think-wrapped finish" {
		t.Fatalf("summary mismatch: %q", got.Summary)
	}
}

// TestObserveResultTagsGatewayBlockedDistinctly verifies that a gateway scope
// rejection (surfaced as a GATEWAY_BLOCKED-prefixed error on the ToolResult) is
// tagged COMMAND_BLOCKED — distinct from a generic TOOL_FAILED — and routes back
// to the step node via edgeIsError.
func TestObserveResultTagsGatewayBlockedDistinctly(t *testing.T) {
	store, _ := NewStore(openTestDB(t))
	task := newRunningTask(t, store, "scan a host")

	s := &GraphState{
		taskID:         task.ID,
		store:          store,
		gate:           NewGate(),
		cfg:            fastRetryCfg(),
		pendingCommand: "nmap -sV 8.8.8.8",
		pendingAct:     Action{StepID: "s1"},
	}

	edge := observeResult(s, ToolResult{
		IsError: true,
		Content: `GATEWAY_BLOCKED: target "8.8.8.8" is not in scope`,
	})
	if edge != edgeIsError {
		t.Fatalf("expected edgeIsError, got %s", edge)
	}
	if !strings.Contains(s.lastObs, "COMMAND_BLOCKED") {
		t.Fatalf("expected COMMAND_BLOCKED tag, got: %s", s.lastObs)
	}
}

// TestObserveResultRecordsCommandOnPlanStep proves markStepStatus records the
// actual command it ran on the plan step — needed so finishNode can later
// cross-check the model's own claimed findings against which tools' commands
// really completed (see unverifiedToolWarning). Before this, PlanStep had no
// record at all of what was literally run, only the model's own natural-
// language step description.
func TestObserveResultRecordsCommandOnPlanStep(t *testing.T) {
	store, _ := NewStore(openTestDB(t))
	task := newRunningTask(t, store, "scan a host")

	s := &GraphState{
		taskID:         task.ID,
		store:          store,
		gate:           NewGate(),
		cfg:            fastRetryCfg(),
		pendingCommand: "nmap -sV 172.20.0.6",
		pendingAct:     Action{StepID: "s1"},
	}
	observeResult(s, ToolResult{Content: "Nmap scan report..."})

	got, _ := store.Get(task.ID)
	if planStep(got.Plan, "s1").Command != "nmap -sV 172.20.0.6" {
		t.Fatalf("expected step to record the command, got %+v", planStep(got.Plan, "s1"))
	}
}

// TestObserveResultVerifiesFindingAgainstLiveOutput proves observeResult
// checks a RUN_COMMAND's own findings against the untruncated result.Content
// of the command that just ran — the fast path in verifyFindingDetail —
// rather than waiting until the (500-char-capped) persisted PlanStep.Result
// exists.
func TestObserveResultVerifiesFindingAgainstLiveOutput(t *testing.T) {
	store, _ := NewStore(openTestDB(t))
	task := newRunningTask(t, store, "scan a host")

	s := &GraphState{
		taskID:         task.ID,
		store:          store,
		gate:           NewGate(),
		cfg:            fastRetryCfg(),
		pendingCommand: "nmap -sV 172.20.0.6",
		pendingAct: Action{
			StepID: "s1",
			FindingsDetail: []FindingDetail{
				{Title: "MySQL exposed", Evidence: "3306/tcp open mysql 5.5.47"}, // no step_id — defaults to s1
			},
		},
	}
	observeResult(s, ToolResult{Content: "PORT   STATE SERVICE\n3306/tcp open mysql 5.5.47\nNmap done."})

	got, _ := store.Get(task.ID)
	if len(got.FindingRecords) != 1 {
		t.Fatalf("expected 1 finding record, got %d: %+v", len(got.FindingRecords), got.FindingRecords)
	}
	if got.FindingRecords[0].Verdict != VerdictVerified {
		t.Fatalf("expected verified (defaulted step_id + live content match), got %+v", got.FindingRecords[0])
	}
	if got.FindingRecords[0].StepID != "s1" {
		t.Fatalf("expected the empty step_id to default to the current step, got %q", got.FindingRecords[0].StepID)
	}
}

// TestMarkStepStatusFallsBackToFirstPendingStepOnUnknownID is a regression
// test for a live-observed bug: a completed multi-step task (real findings,
// status=completed) still showed EVERY plan step frozen at "pending" — the
// operator-facing plan view looked like nothing had happened at all, despite
// real work having completed successfully. Root cause: markStepStatus only
// updates a step whose ID exactly matches pendingAct.StepID; when the model
// supplies a StepID that doesn't match any plan step (or omits it), the
// lookup silently matches nothing and the plan is never touched — with no
// fallback. The prompt clearly shows each step's ID, but a model's own
// bookkeping compliance can't be guaranteed, so the tracking must degrade
// gracefully: fall back to the first non-terminal (pending/running) step
// instead of doing nothing.
func TestMarkStepStatusFallsBackToFirstPendingStepOnUnknownID(t *testing.T) {
	store, _ := NewStore(openTestDB(t))
	task, err := store.Create("test-session", "multi-step goal", 10)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.Transition(task.ID, StatusPlanned, func(tk *Task) {
		tk.Plan = []PlanStep{
			{ID: "s1", Description: "first step", Status: StepPending},
			{ID: "s2", Description: "second step", Status: StepPending},
		}
	}); err != nil {
		t.Fatalf("transition to planned: %v", err)
	}
	if err := store.Transition(task.ID, StatusRunning, nil); err != nil {
		t.Fatalf("transition to running: %v", err)
	}

	s := &GraphState{
		taskID:         task.ID,
		store:          store,
		gate:           NewGate(),
		cfg:            fastRetryCfg(),
		pendingCommand: "curl -sI http://target",
		// The model gave a StepID that doesn't match any plan step — this
		// happens in practice with weaker instruction-following.
		pendingAct: Action{StepID: "step-1-not-a-real-id"},
	}

	edge := observeResult(s, ToolResult{Content: "HTTP/1.1 200 OK"})
	if edge != edgeOK {
		t.Fatalf("expected edgeOK, got %s", edge)
	}

	got, _ := store.Get(task.ID)
	if planStep(got.Plan, "s1").Status != StepDone {
		t.Fatalf("expected the first pending step (s1) to be marked done as a fallback, got plan=%+v", got.Plan)
	}
	if planStep(got.Plan, "s2").Status != StepPending {
		t.Fatalf("expected s2 to remain untouched, got %s", planStep(got.Plan, "s2").Status)
	}
}

// TestIsStuckSlidingWindow tests stuck detection without any LLM or exec.
func TestIsStuckSlidingWindow(t *testing.T) {
	// Three identical commands → stuck.
	w1 := []string{"aabb", "ccdd", "aabb", "ccdd", "aabb", "ccdd"}
	if !isStuck(w1) {
		t.Error("expected A→B→A→B cycle to be detected as stuck")
	}

	// Three of the same hash in window → stuck.
	w2 := []string{"aaaa", "bbbb", "aaaa", "cccc", "aaaa"}
	if !isStuck(w2) {
		t.Error("expected 3-repeat to be detected as stuck")
	}

	// Unique commands → not stuck.
	w3 := []string{"aaaa", "bbbb", "cccc", "dddd"}
	if isStuck(w3) {
		t.Error("unique commands must not trigger stuck detection")
	}

	// Too few commands → not stuck.
	w4 := []string{"aaaa", "aaaa"}
	if isStuck(w4) {
		t.Error("window too small should not trigger stuck")
	}
}

// TestStripThinkTokens verifies the think-token stripping function.
func TestStripThinkTokens(t *testing.T) {
	cases := []struct {
		name, input, want string
	}{
		{
			"single block",
			"<think>internal reasoning</think>actual response",
			"actual response",
		},
		{
			"multi-line block",
			"<think>\nline 1\nline 2\n</think>\n{\"action\":\"finish\"}",
			"\n{\"action\":\"finish\"}",
		},
		{
			"no think tokens",
			`{"action":"finish","summary":"ok"}`,
			`{"action":"finish","summary":"ok"}`,
		},
		{
			"unclosed tag",
			"prefix<think>everything after is removed",
			"prefix",
		},
		{
			"nested double",
			"<think>a</think>middle<think>b</think>end",
			"middleend",
		},
	}
	for _, c := range cases {
		got := stripThinkTokens(c.input)
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// TestCompressObservationBoundaries verifies head+tail preservation.
func TestCompressObservationBoundaries(t *testing.T) {
	small := strings.Repeat("x", 100)
	if CompressObservation(small) != small {
		t.Error("small output must pass through unmodified")
	}

	// Large output: verify head is preserved, tail is preserved, marker present.
	large := strings.Repeat("A", 1500) + "\n" + strings.Repeat("B", 1500)
	compressed := CompressObservation(large)
	if !strings.Contains(compressed, "...") {
		t.Error("compressed output must contain the '...' marker")
	}
	if !strings.HasPrefix(compressed, strings.Repeat("A", 50)) {
		t.Error("head of large output must be preserved")
	}
	if !strings.HasSuffix(strings.TrimSpace(compressed), strings.Repeat("B", 50)) {
		t.Error("tail of large output must be preserved")
	}
}

// TestEstimateTokensRoughly verifies EstimateTokens returns a reasonable value.
func TestEstimateTokensRoughly(t *testing.T) {
	// 400 chars → ~100 tokens
	text := strings.Repeat("a", 400)
	tokens := EstimateTokens(text)
	if tokens < 90 || tokens > 110 {
		t.Errorf("expected ~100 tokens for 400 chars, got %d", tokens)
	}
	// Empty string → 0 tokens
	if EstimateTokens("") != 0 {
		t.Error("empty string should estimate 0 tokens")
	}
}
