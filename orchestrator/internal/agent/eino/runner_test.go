package eino

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	osexec "os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/n0connect/yummi/orchestrator/internal/agent"
	"github.com/n0connect/yummi/orchestrator/internal/execution"
)

func TestEinoRunner_SatisfiesTaskRunner(t *testing.T) {
	var _ agent.TaskRunner = (*EinoRunner)(nil)
}

func TestEinoRunner_CreateAndPlanPersistsRecoveryAnchor(t *testing.T) {
	store, _ := agent.NewStore(nil)
	r := NewEinoRunner(store, nil, nil, Config{})
	task, err := r.CreateAndPlan(context.Background(), "session-1", "recoverable goal")
	if err != nil {
		t.Fatalf("CreateAndPlan: %v", err)
	}
	if len(task.Plan) == 0 || task.Plan[0].Status != agent.StepPending {
		t.Fatalf("Eino task has no durable recovery anchor: %+v", task.Plan)
	}
}

func TestEinoRunner_SetApprovalModePropagatesAndReportsCapabilities(t *testing.T) {
	store, _ := agent.NewStore(nil)
	sub := agent.NewRunner(store, nil, nil, nil, agent.Config{})
	r := NewEinoRunner(store, nil, nil, Config{SubAgents: map[string]*agent.Runner{"web": sub}})
	if err := r.SetApprovalMode(agent.ModeAuto); err != nil {
		t.Fatal(err)
	}
	if sub.ApprovalMode() != agent.ModeAuto {
		t.Fatalf("sub-agent mode = %s, want auto", sub.ApprovalMode())
	}
	caps := r.Capabilities()
	if caps.Backend != "eino" || !caps.Resumable || !caps.NativeToolCalling || len(caps.Roles) != 1 {
		t.Fatalf("unexpected capabilities: %+v", caps)
	}
}

func TestEinoRunner_ResumeRecoveredReconstructsNativeRun(t *testing.T) {
	srv := toolCallServer("finish", `{"summary":"recovered safely","findings":[]}`)
	defer srv.Close()
	db := openTestDB(t)
	before, _ := agent.NewStore(db)
	r1 := NewEinoRunner(before, nil, nil, Config{ModelBaseURL: srv.URL + "/v1", ModelName: "test"})
	task, err := r1.CreateAndPlan(context.Background(), "recover-session", "finish after restart")
	if err != nil {
		t.Fatal(err)
	}
	if err := before.Transition(task.ID, agent.StatusRunning, nil); err != nil {
		t.Fatal(err)
	}

	after, err := agent.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	recovered, _ := after.Get(task.ID)
	if recovered.Status != agent.StatusPaused || !recovered.Checkpoint.ResumeAfterRestart {
		t.Fatalf("task not recoverable: %+v", recovered)
	}
	r2 := NewEinoRunner(after, nil, nil, Config{ModelBaseURL: srv.URL + "/v1", ModelName: "test"})
	count, err := r2.ResumeRecovered()
	if err != nil || count != 1 {
		t.Fatalf("ResumeRecovered count=%d err=%v", count, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		final, _ := after.Get(task.ID)
		if final.Status == agent.StatusCompleted {
			if final.Summary != "recovered safely" {
				t.Fatalf("unexpected summary: %q", final.Summary)
			}
			return
		}
		if final.Status == agent.StatusFailed {
			t.Fatalf("recovered task failed: %s", final.LastError)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for recovered Eino task")
}

// TestEinoRunner_Run_CallsFinishAndCompletesTask scripts a single-turn
// tool-calling response (finish, with no other tool calls) against a real
// httptest server shaped like Ollama's OpenAI-compatible endpoint — no mock
// of Yummi's own code, matching project convention.
func TestEinoRunner_Run_CallsFinishAndCompletesTask(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "1", "object": "chat.completion", "model": "test",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role": "assistant", "content": "",
					"tool_calls": []map[string]any{{
						"id": "call_1", "type": "function",
						"function": map[string]any{
							"name":      "finish",
							"arguments": `{"summary":"done: found nothing of note","findings":[]}`,
						},
					}},
				},
				"finish_reason": "tool_calls",
			}},
		})
	}))
	defer srv.Close()

	store, err := agent.NewStore(nil) // in-memory, matches project's ":memory:" convention
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	r := NewEinoRunner(store, nil, nil, Config{
		ModelBaseURL: srv.URL + "/v1", ModelName: "test", ApprovalMode: agent.ModeAuto,
	})

	task, err := r.CreateAndPlan(context.Background(), "session-1", "test goal")
	if err != nil {
		t.Fatalf("CreateAndPlan: %v", err)
	}
	if task.Status != agent.StatusPlanned {
		t.Fatalf("status after CreateAndPlan = %s, want planned", task.Status)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Run(ctx, &task); err != nil {
		t.Fatalf("Run: %v", err)
	}

	final, _ := store.Get(task.ID)
	if final.Status != agent.StatusCompleted {
		t.Fatalf("status = %s, want completed", final.Status)
	}
	if len(final.Findings) != 0 {
		t.Fatalf("findings = %v, want none (empty array in the finish call)", final.Findings)
	}
}

// toolCallServer serves an OpenAI-shaped chat-completion response calling
// exactly one tool, repeated for every request it receives — used both for a
// single-turn scripted call and (with a small StepBudget) to force
// MaxIterations exhaustion deterministically.
func toolCallServer(toolName, argsJSON string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "1", "object": "chat.completion", "model": "test",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role": "assistant", "content": "",
					"tool_calls": []map[string]any{{
						"id": "call_1", "type": "function",
						"function": map[string]any{"name": toolName, "arguments": argsJSON},
					}},
				},
				"finish_reason": "tool_calls",
			}},
		})
	}))
}

func TestEinoRunner_Run_FindingsFromFinishArePersisted(t *testing.T) {
	srv := toolCallServer("finish", `{"summary":"done","findings":["port 22 open","nginx 1.18"]}`)
	defer srv.Close()

	store, _ := agent.NewStore(nil)
	r := NewEinoRunner(store, nil, nil, Config{
		ModelBaseURL: srv.URL + "/v1", ModelName: "test", ApprovalMode: agent.ModeAuto,
	})
	task, err := r.CreateAndPlan(context.Background(), "session-1", "test goal")
	if err != nil {
		t.Fatalf("CreateAndPlan: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Run(ctx, &task); err != nil {
		t.Fatalf("Run: %v", err)
	}
	final, _ := store.Get(task.ID)
	if final.Status != agent.StatusCompleted {
		t.Fatalf("status = %s, want completed", final.Status)
	}
	if len(final.Findings) != 2 || final.Findings[0] != "port 22 open" || final.Findings[1] != "nginx 1.18" {
		t.Fatalf("findings = %v, want the two findings from the finish call", final.Findings)
	}
}

// TestEinoRunner_Run_AskOperatorPausesTask proves ask_operator actually stops
// the ReAct loop (via ReturnDirectly) and pauses the task, mirroring
// graph.go's askNode — before this, ask_operator's result was just fed back
// for another model turn with nothing distinguishing it from a normal tool.
func TestEinoRunner_Run_AskOperatorPausesTask(t *testing.T) {
	srv := toolCallServer("ask_operator", `{"question":"which credential set should I try?"}`)
	defer srv.Close()

	store, _ := agent.NewStore(nil)
	r := NewEinoRunner(store, nil, nil, Config{
		ModelBaseURL: srv.URL + "/v1", ModelName: "test", ApprovalMode: agent.ModeAuto,
	})
	task, err := r.CreateAndPlan(context.Background(), "session-1", "test goal")
	if err != nil {
		t.Fatalf("CreateAndPlan: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Run(ctx, &task); err != nil {
		t.Fatalf("Run: %v", err)
	}
	final, _ := store.Get(task.ID)
	if final.Status != agent.StatusPaused {
		t.Fatalf("status = %s, want paused", final.Status)
	}
	if final.Question != "which credential set should I try?" {
		t.Fatalf("Question = %q, want the asked question", final.Question)
	}
}

// TestEinoRunner_Start_ResumeCarriesOperatorAnswerIntoNextRun proves the
// operator's answer (submitted via Start's message param, exactly like the
// UI's resumeTask does against POST /tasks/{id}/start) reaches the model on
// resume — Eino's ChatModelAgent keeps no persisted conversation state
// between Run() calls, so without this a resumed task would restart from the
// bare goal with no idea it ever asked a question.
func TestEinoRunner_Start_ResumeCarriesOperatorAnswerIntoNextRun(t *testing.T) {
	var mu sync.Mutex
	var requestBodies []string
	turn := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requestBodies = append(requestBodies, string(body))
		t := turn
		turn++
		mu.Unlock()

		var toolCall map[string]any
		if t == 0 {
			toolCall = map[string]any{
				"id": "call_1", "type": "function",
				"function": map[string]any{"name": "ask_operator", "arguments": `{"question":"proceed with a noisy scan?"}`},
			}
		} else {
			toolCall = map[string]any{
				"id": "call_2", "type": "function",
				"function": map[string]any{"name": "finish", "arguments": `{"summary":"done","findings":[]}`},
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "1", "object": "chat.completion", "model": "test",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role": "assistant", "content": "", "tool_calls": []map[string]any{toolCall},
				},
				"finish_reason": "tool_calls",
			}},
		})
	}))
	defer srv.Close()

	store, _ := agent.NewStore(nil)
	r := NewEinoRunner(store, nil, nil, Config{
		ModelBaseURL: srv.URL + "/v1", ModelName: "test", ApprovalMode: agent.ModeAuto,
	})
	task, err := r.CreateAndPlan(context.Background(), "session-1", "assess the target")
	if err != nil {
		t.Fatalf("CreateAndPlan: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Run(ctx, &task); err != nil {
		t.Fatalf("Run: %v", err)
	}
	paused, _ := store.Get(task.ID)
	if paused.Status != agent.StatusPaused {
		t.Fatalf("status after first run = %s, want paused", paused.Status)
	}

	if err := r.Start(task.ID, "yes, go ahead with -T4"); err != nil {
		t.Fatalf("Start (resume): %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var final agent.Task
	for time.Now().Before(deadline) {
		final, _ = store.Get(task.ID)
		if final.Status.Terminal() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if final.Status != agent.StatusCompleted {
		t.Fatalf("status after resume = %s, want completed", final.Status)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requestBodies) < 2 {
		t.Fatalf("expected at least 2 requests (pause + resume), got %d", len(requestBodies))
	}
	resumeReq := requestBodies[1]
	if !strings.Contains(resumeReq, "proceed with a noisy scan") || !strings.Contains(resumeReq, "yes, go ahead with -T4") {
		t.Fatalf("resume request missing prior question/operator answer:\n%s", resumeReq)
	}
}

// TestEinoRunner_Run_ExceedsMaxIterations_FailsTask proves StepBudget is now
// really enforced (adk.ChatModelAgentConfig.MaxIterations previously stayed
// at Eino's own hardcoded default of 20 regardless of StepBudget) and that a
// run-time error from the ADK (adk.ErrExceedMaxIterations) fails the task
// instead of silently completing it with a stale/empty summary.
func TestEinoRunner_Run_ExceedsMaxIterations_FailsTask(t *testing.T) {
	// remember never blocks on the exec pipeline, so this loops purely on
	// LLM round-trips — deterministic and fast.
	srv := toolCallServer("remember", `{"key":"k","value":"v"}`)
	defer srv.Close()

	store, _ := agent.NewStore(nil)
	r := NewEinoRunner(store, nil, nil, Config{
		StepBudget: 2, ModelBaseURL: srv.URL + "/v1", ModelName: "test", ApprovalMode: agent.ModeAuto,
	})
	task, err := r.CreateAndPlan(context.Background(), "session-1", "test goal")
	if err != nil {
		t.Fatalf("CreateAndPlan: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Run(ctx, &task); err == nil {
		t.Fatal("Run: expected an error when MaxIterations is exceeded, got nil")
	}
	final, _ := store.Get(task.ID)
	if final.Status != agent.StatusFailed {
		t.Fatalf("status = %s, want failed", final.Status)
	}
	if final.LastError == "" {
		t.Fatal("expected LastError to be set")
	}
}

// realShellStore mirrors package agent's own unexported test helper of the
// same name (a different package, so not directly reusable): a real
// execution.Store backed by an actual shell, needed so an approved
// sub-agent RUN_COMMAND has something real to execute and observe.
func realShellStore(t *testing.T) *execution.Store {
	t.Helper()
	es := execution.NewStore()
	es.SetProcessor(func(ctx context.Context, rec execution.Record) (string, error) {
		out, err := osexec.CommandContext(ctx, "sh", "-c", rec.Command).CombinedOutput()
		return string(out), err
	})
	return es
}

// TestEinoRunner_Approve_ResolvesSubAgentGate is a live-E2E-found regression
// test: a delegated sub-agent runs its own task loop against its own private
// *agent.Gate (it's a graph Runner — see RunSubtask's doc), but the only API
// path (HandleApproveTask -> the top-level TaskRunner.Approve) originally
// called only EinoRunner's own (empty) gate, so a delegated sub-agent's
// RUN_COMMAND could never be approved through the API — it would always
// time out. Found running a real handoff against a real qwen3:14b model:
// the sub-agent correctly reached StatusAwaitingApproval, but approving
// through the top-level Eino task silently did nothing. This proves the
// fix (mirroring graph.Runner.Approve's own sub-agent fallback loop).
func TestEinoRunner_Approve_ResolvesSubAgentGate(t *testing.T) {
	store, err := agent.NewStore(openTestDB(t))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	execer := realShellStore(t)

	sub := agent.NewRunner(store, scriptedOllama(t,
		`{"steps":[{"id":"s1","description":"do a risky thing"}]}`,
		`[RUN_COMMAND]
{"command":"echo risky","step_id":"s1","rationale":"test","risk_level":"medium"}`,
		`[FINISH]
{"summary":"sub done"}`,
	), nil, agent.NewStoreExecer(execer, 5*time.Second), agent.Config{StepBudget: 5, ApprovalMode: agent.ModeAskPermission})

	// First call hands off; every call after (the handoff tool's own result
	// feeds back for another turn) finishes — the fake model doesn't need to
	// "notice" a completed delegation, it just always closes out on round 2+.
	turn := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var toolCall map[string]any
		if turn == 0 {
			toolCall = map[string]any{
				"id": "call_1", "type": "function",
				"function": map[string]any{"name": "handoff", "arguments": `{"role":"web","subgoal":"do the risky thing"}`},
			}
		} else {
			toolCall = map[string]any{
				"id": "call_2", "type": "function",
				"function": map[string]any{"name": "finish", "arguments": `{"summary":"delegated and done"}`},
			}
		}
		turn++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "1", "object": "chat.completion", "model": "test",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role": "assistant", "content": "", "tool_calls": []map[string]any{toolCall},
				},
				"finish_reason": "tool_calls",
			}},
		})
	}))
	defer srv.Close()

	r := NewEinoRunner(store, nil, nil, Config{
		ModelBaseURL: srv.URL + "/v1", ModelName: "test", ApprovalMode: agent.ModeAuto,
		SubAgents: map[string]*agent.Runner{"web": sub},
	})

	task, err := r.CreateAndPlan(context.Background(), "sess-sub-approve", "delegate risky work")
	if err != nil {
		t.Fatalf("CreateAndPlan: %v", err)
	}

	runErrCh := make(chan error, 1)
	go func() { runErrCh <- r.Run(context.Background(), &task) }()

	var subTask agent.Task
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, tk := range store.ListBySession("sess-sub-approve", 50) {
			if tk.Status == agent.StatusAwaitingApproval {
				subTask = tk
			}
		}
		if subTask.ID != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if subTask.ID == "" {
		t.Fatal("sub-agent task never reached awaiting_approval")
	}

	// The regression check: approving through the TOP-LEVEL (Eino) runner —
	// the only one the API has a handle to — must resolve the sub-agent's
	// gate, not just Eino's own (empty) one.
	if err := r.Approve(subTask.ID, subTask.PendingProposalID, true, ""); err != nil {
		t.Fatalf("r.Approve on sub-agent task: %v", err)
	}

	select {
	case err := <-runErrCh:
		if err != nil {
			t.Fatalf("r.Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("orchestrator task did not complete after approval")
	}
	final, _ := store.Get(task.ID)
	if final.Status != agent.StatusCompleted {
		t.Fatalf("orchestrator task not completed: %s (lastErr=%q)", final.Status, final.LastError)
	}
}
