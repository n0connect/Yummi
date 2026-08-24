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

func TestEinoRunner_StopClearsPendingApprovalFields(t *testing.T) {
	store, _ := agent.NewStore(nil)
	task, _ := store.Create("session-1", "stop pending task", 5)
	_ = store.Transition(task.ID, agent.StatusPlanned, nil)
	_ = store.Transition(task.ID, agent.StatusRunning, nil)
	_ = store.Transition(task.ID, agent.StatusAwaitingApproval, func(tk *agent.Task) {
		tk.PendingProposalID = "proposal-1"
		tk.PendingCommand = "hostname"
		tk.PendingRisk = "low"
	})
	r := NewEinoRunner(store, nil, nil, Config{})

	if err := r.Stop(task.ID); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	got, _ := store.Get(task.ID)
	if got.Status != agent.StatusStopped {
		t.Fatalf("status = %s, want stopped", got.Status)
	}
	if got.PendingProposalID != "" || got.PendingCommand != "" || got.PendingRisk != "" {
		t.Fatalf("stopped task retained pending approval state: %+v", got)
	}
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

// TestEinoRunner_Instruction_RunCommandIsUnrestricted is a live-long-form-
// testing-found regression test: a real model reproducibly (2/2 identical
// prompts) hallucinated a tool restriction that doesn't exist — "the
// provided functions do not support running standard system commands like
// ps" — for an ordinary process-listing goal, then reported the task
// complete having run nothing at all. The prior wording ("use run_command
// only for things that aren't pentest tools at all") read as a narrow
// allowlist rather than the preference-ordering it was meant to be. Locks
// the corrected, unambiguous wording in place.
func TestEinoRunner_Instruction_RunCommandIsUnrestricted(t *testing.T) {
	r := &EinoRunner{cfg: Config{}}
	instr := r.instruction("list running processes")
	if !strings.Contains(instr, "run_command executes ANY shell command in the sandbox and is always available") {
		t.Fatalf("instruction does not state run_command is unrestricted:\n%s", instr)
	}
	if strings.Contains(instr, "only for things that aren't") {
		t.Fatalf("instruction still contains the ambiguous allowlist-shaped wording:\n%s", instr)
	}
}

// TestEinoRunner_Run_EmptyFinishSummaryFallsBackToTraceEvidence is a live-
// long-form-testing-found regression test: a real 3-part task completed
// every part (confirmed via the execution records — find, nproc, getent
// passwd all ran and returned real data) yet finish was still called with an
// empty summary, even with the instruction telling the model to call finish
// alone. Relying on instruction compliance alone isn't enough for a 14B
// model — this proves the code-level fallback: when summary is empty but
// real tool results were recorded, the task's persisted Summary is
// synthesized from that evidence instead of being left blank.
func TestEinoRunner_Run_EmptyFinishSummaryFallsBackToTraceEvidence(t *testing.T) {
	var turn int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		turn++
		var toolCall map[string]any
		if turn == 1 {
			toolCall = map[string]any{
				"id": "call_1", "type": "function",
				"function": map[string]any{
					"name": "run_command", "arguments": `{"command":"nproc","rationale":"count cores","risk_level":"low"}`,
				},
			}
		} else {
			// Called alone, per instruction — but with an empty summary anyway.
			toolCall = map[string]any{
				"id": "call_2", "type": "function",
				"function": map[string]any{"name": "finish", "arguments": `{"summary":"","findings":[]}`},
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "1", "object": "chat.completion", "model": "test",
			"choices": []map[string]any{{"index": 0, "message": map[string]any{
				"role": "assistant", "content": "", "tool_calls": []map[string]any{toolCall},
			}, "finish_reason": "tool_calls"}},
		})
	}))
	defer srv.Close()

	store, _ := agent.NewStore(nil)
	exec := &fakeExecer{ran: make(chan string, 1), content: "2"}
	r := NewEinoRunner(store, nil, exec, Config{
		ModelBaseURL: srv.URL + "/v1", ModelName: "test", ApprovalMode: agent.ModeAuto,
	})
	task, err := r.CreateAndPlan(context.Background(), "session-1", "count cpu cores")
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
	if strings.TrimSpace(final.Summary) == "" {
		t.Fatal("Summary is empty despite real trace evidence — fallback did not fire")
	}
	if !strings.Contains(final.Summary, "2") {
		t.Fatalf("Summary does not contain the actual tool result: %q", final.Summary)
	}
}

// TestEinoRunner_Instruction_FinishMustBeCalledAlone is a live-long-form-
// testing-found regression test: tasks were observed completing with an
// EMPTY summary despite real evidence having just been gathered (~2 of 9
// completed tasks in one testing session). Root cause: when the model
// batches a data-gathering tool call and finish in the SAME turn, finish's
// arguments are drafted before the other tool has actually run — so there is
// nothing real yet to cite, and the anti-fabrication instinct correctly (but
// unhelpfully) leaves the summary empty. Locks the corrected instruction
// telling the model to call finish alone, in its own turn.
func TestEinoRunner_Instruction_FinishMustBeCalledAlone(t *testing.T) {
	r := &EinoRunner{cfg: Config{}}
	instr := r.instruction("some goal")
	if !strings.Contains(instr, "never call finish in the same turn as another tool") {
		t.Fatalf("instruction does not tell the model to call finish alone:\n%s", instr)
	}
}

// TestEinoRunner_Run_InstructionCarriesConfiguredPersona is a structural
// regression test: unlike the graph backend (agent.Runner, which renders
// AgentPersona via StepSystemPrompt), this backend previously had NO Persona
// field at all — instruction() was a hardcoded string covering only tool-
// routing mechanics, so none of agent.yaml's ORIENT/ACT/OBSERVE/CONCLUDE
// epistemic-discipline principles (NO FABRICATION, EVIDENCE, TRUTH, ...) ever
// reached the model here. This is very plausibly why live E2E runs against
// this backend saw failure modes the graph backend's own persona explicitly
// guards against (accepting an unverified premise as fact, inventing tool
// output, denying an actual hallucination). Proves the configured Persona's
// identity/principles text actually appears in the outbound LLM request.
func TestEinoRunner_Run_InstructionCarriesConfiguredPersona(t *testing.T) {
	var reqBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		reqBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "1", "object": "chat.completion", "model": "test",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role": "assistant", "content": "",
					"tool_calls": []map[string]any{{
						"id": "call_1", "type": "function",
						"function": map[string]any{"name": "finish", "arguments": `{"summary":"done","findings":[]}`},
					}},
				},
				"finish_reason": "tool_calls",
			}},
		})
	}))
	defer srv.Close()

	store, _ := agent.NewStore(nil)
	r := NewEinoRunner(store, nil, nil, Config{
		ModelBaseURL: srv.URL + "/v1", ModelName: "test", ApprovalMode: agent.ModeAuto,
		Persona: agent.AgentPersona{
			Identity:   "You are Yummi, distinctive-identity-marker-xyz.",
			Principles: "NO FABRICATION: distinctive-principle-marker-abc.",
		},
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
	if !strings.Contains(reqBody, "distinctive-identity-marker-xyz") {
		t.Fatalf("outbound request does not carry the configured Persona identity:\n%s", reqBody)
	}
	if !strings.Contains(reqBody, "distinctive-principle-marker-abc") {
		t.Fatalf("outbound request does not carry the configured Persona principles:\n%s", reqBody)
	}
}

// TestEinoRunner_Run_SequentialToolDispatchAvoidsApprovalRace is a live-E2E-
// found regression test: eino's compose.ToolsNodeConfig defaults to running
// every tool call in one model turn concurrently, but agent.Gate holds only
// one pending approval per task. Two run_command calls emitted in the same
// assistant turn used to race Gate.Request — whichever Request lost the race
// had its verdict channel silently orphaned while the Task's persisted
// PendingProposalID reflected only one of the two, so approving the exact
// proposal/command pair the UI displayed could 404 with "no pending
// approval". This proves ToolsNodeConfig.ExecuteSequentially keeps dispatch
// (and therefore gating) one-at-a-time: an approver that resolves whatever is
// currently pending, one at a time, never errors and both commands run.
func TestEinoRunner_Run_SequentialToolDispatchAvoidsApprovalRace(t *testing.T) {
	var mu sync.Mutex
	reqN := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		reqN++
		n := reqN
		mu.Unlock()

		msg := map[string]any{
			"role": "assistant", "content": "",
			"tool_calls": []map[string]any{{
				"id": "call_finish", "type": "function",
				"function": map[string]any{"name": "finish", "arguments": `{"summary":"done","findings":[]}`},
			}},
		}
		if n == 1 {
			msg = map[string]any{
				"role": "assistant", "content": "",
				"tool_calls": []map[string]any{
					{"id": "call_1", "type": "function", "function": map[string]any{
						"name": "run_command", "arguments": `{"command":"echo one","rationale":"r1","risk_level":"low"}`,
					}},
					{"id": "call_2", "type": "function", "function": map[string]any{
						"name": "run_command", "arguments": `{"command":"echo two","rationale":"r2","risk_level":"low"}`,
					}},
				},
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "1", "object": "chat.completion", "model": "test",
			"choices": []map[string]any{{"index": 0, "message": msg, "finish_reason": "tool_calls"}},
		})
	}))
	defer srv.Close()

	store, err := agent.NewStore(nil)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	exec := &fakeExecer{ran: make(chan string, 2)}
	r := NewEinoRunner(store, nil, exec, Config{
		ModelBaseURL: srv.URL + "/v1", ModelName: "test", ApprovalMode: agent.ModeAskPermission,
	})

	task, err := r.CreateAndPlan(context.Background(), "session-1", "test goal")
	if err != nil {
		t.Fatalf("CreateAndPlan: %v", err)
	}

	approveErrs := make(chan error, 2)
	done := make(chan struct{})
	go func() {
		defer close(approveErrs)
		seen := map[string]bool{}
		deadline := time.Now().Add(4 * time.Second)
		for len(seen) < 2 && time.Now().Before(deadline) {
			select {
			case <-done:
				return
			default:
			}
			pending, ok := r.gate.Pending(task.ID)
			if !ok || seen[pending.ProposalID] {
				time.Sleep(5 * time.Millisecond)
				continue
			}
			seen[pending.ProposalID] = true
			approveErrs <- r.Approve(task.ID, pending.ProposalID, true, "approved")
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	runErr := r.Run(ctx, &task)
	close(done)
	if runErr != nil {
		t.Fatalf("Run: %v", runErr)
	}

	for aerr := range approveErrs {
		if aerr != nil {
			t.Fatalf("Approve: %v (this is exactly the live 404 \"no pending approval\" symptom)", aerr)
		}
	}

	var ran []string
	for range 2 {
		select {
		case cmd := <-exec.ran:
			ran = append(ran, cmd)
		default:
		}
	}
	if len(ran) != 2 {
		t.Fatalf("commands executed = %v, want both echo one and echo two to have run", ran)
	}
}

// TestEinoRunner_Run_StalledModelCallFailsRatherThanHangingForever is a
// live-long-form-testing-found regression test: NewOllamaChatModel's
// underlying openai.ChatModelConfig left Timeout at its zero value, which
// openai-ext turns into &http.Client{Timeout: 0} — Go's convention for NO
// timeout at all. A live run reproduced a task stuck at status=running,
// steps_used=0, for 10+ minutes with zero progress and no error, while
// Ollama itself was reachable and responsive to other requests — the
// model's own HTTP client had no bound, so a single stalled call hung the
// task's goroutine indefinitely (runCtx is only ever cancelled by an
// explicit Stop() or process shutdown). Proves a short configured
// ModelTimeout turns that permanent hang into a bounded, visible task
// failure instead.
func TestEinoRunner_Run_StalledModelCallFailsRatherThanHangingForever(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block // never respond — simulates a stalled/hung upstream call
	}))
	// Deliberately declared in this order: defers run LIFO, so close(block)
	// (declared second, runs first) unblocks the handler goroutine BEFORE
	// srv.Close() (declared first, runs last) tries to drain connections —
	// otherwise Close() waits out its own internal grace period for the
	// still-active connection, which looks like (but isn't) our ModelTimeout
	// failing to fire.
	defer srv.Close()
	defer close(block)

	store, _ := agent.NewStore(nil)
	r := NewEinoRunner(store, nil, nil, Config{
		ModelBaseURL: srv.URL + "/v1", ModelName: "test", ApprovalMode: agent.ModeAuto,
		ModelTimeout: 500 * time.Millisecond,
	})
	task, err := r.CreateAndPlan(context.Background(), "session-1", "test goal")
	if err != nil {
		t.Fatalf("CreateAndPlan: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	runErr := r.Run(ctx, &task)
	elapsed := time.Since(start)

	if runErr == nil {
		t.Fatal("Run: expected an error from the stalled model call, got nil")
	}
	if elapsed > 3*time.Second {
		t.Fatalf("Run took %s to fail — ModelTimeout did not bound the stalled call", elapsed)
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

func TestShouldNudgeIncompleteGoals(t *testing.T) {
	twoParts := []string{"scan ports", "grab banner"}
	threeParts := []string{"a", "b", "c"}
	cases := []struct {
		name         string
		parts        []string
		covered      int
		alreadyNudged bool
		want         bool
	}{
		{"two parts, one covered -> nudge", twoParts, 1, false, true},
		{"two parts, both covered -> no nudge", twoParts, 2, false, false},
		{"two parts, one covered but already nudged -> no", twoParts, 1, true, false},
		{"three parts, two covered -> nudge", threeParts, 2, false, true},
		{"single-intent goal -> never nudge", []string{"just scan it"}, 0, false, false},
		{"no parts -> never nudge", nil, 0, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := shouldNudgeIncompleteGoals(c.parts, c.covered, c.alreadyNudged); got != c.want {
				t.Fatalf("shouldNudgeIncompleteGoals(%v, %d, %v) = %v, want %v",
					c.parts, c.covered, c.alreadyNudged, got, c.want)
			}
		})
	}
}

func TestBuildIncompleteGoalsMessage_CarriesPartsAndWorkForward(t *testing.T) {
	tr := &runTrace{}
	tr.record("nmap -sV 10.0.0.1", "22/tcp open ssh")
	parts := []string{"scan the ports", "grab the http banner"}
	msg := buildIncompleteGoalsMessage("1) scan the ports 2) grab the http banner", parts, tr)

	for _, want := range []string{"scan the ports", "grab the http banner", "nmap", "22/tcp open ssh", "finish"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("re-run message missing %q\n---\n%s", want, msg)
		}
	}
}

// TestEinoRunner_Run_NaturalLanguageMultiPartGoal_AuditedAndNudged proves the
// AuditGoalParts fallback wired via PlanLLM: a goal with NO numbered markers
// ("check the OS ... and also list the open ports" — real operator phrasing)
// must still be recognized as multi-part and trigger the premature-FINISH
// bounce when finish is reached with no tool results at all. Without PlanLLM,
// agent.SplitEnumeratedParts alone would see zero parts for this goal and the
// guard would never fire — this is the regression test for that gap.
func TestEinoRunner_Run_NaturalLanguageMultiPartGoal_AuditedAndNudged(t *testing.T) {
	reactSrv := toolCallServer("finish", `{"summary":"the OS is Linux","findings":["Linux kernel 5.x"]}`)
	defer reactSrv.Close()

	store, err := agent.NewStore(openTestDB(t))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	r := NewEinoRunner(store, nil, nil, Config{
		ModelBaseURL: reactSrv.URL + "/v1", ModelName: "test", ApprovalMode: agent.ModeAuto,
		PlanLLM: scriptedOllama(t, "check what OS is running\nlist the open ports"),
	})

	task, err := r.CreateAndPlan(context.Background(), "session-1", "check what OS is running and also list the open ports")
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
		t.Fatalf("status = %s, want completed (task should still finish after one bounce)", final.Status)
	}

	nudged := false
	for _, ev := range store.ListActivity(task.SessionID, 50) {
		if strings.Contains(ev.Summary, "goal parts covered") {
			nudged = true
		}
	}
	if !nudged {
		t.Fatalf("expected the incomplete-goals nudge activity — PlanLLM's audit fallback did not populate enumParts for an unmarked multi-part goal")
	}
}

// TestEinoRunner_Run_SanitizesRawSpecialTokensInFallbackSummary is the
// regression test for a live-observed leak: when a model's "finish" intent
// isn't cleanly routed into a tool_calls entry, Run() falls back to the raw
// assistant message content (lastMessage.Content). Found running gpt-oss-20b
// through LM Studio — that fallback content was, verbatim, the model's own
// unparsed Harmony-format control tokens ("<|channel|>final <|message|>...")
// wrapping the real answer, and it went straight into the wire-visible
// Task.Summary unsanitized. The graph backend has sanitized every
// model-authored string since day one (agent.SanitizeAgentText); this
// backend never did until now.
func TestEinoRunner_Run_SanitizesRawSpecialTokensInFallbackSummary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "1", "object": "chat.completion", "model": "test",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role": "assistant",
					"content": "<|channel|>final <|constrain|>finish<|channel|>commentary json<|message|>" +
						`{"summary":"the host has 2 CPU cores","findings":[]}`,
				},
				"finish_reason": "stop",
			}},
		})
	}))
	defer srv.Close()

	store, _ := agent.NewStore(nil)
	r := NewEinoRunner(store, nil, nil, Config{
		ModelBaseURL: srv.URL + "/v1", ModelName: "test", ApprovalMode: agent.ModeAuto,
	})
	task, err := r.CreateAndPlan(context.Background(), "session-1", "count cpu cores")
	if err != nil {
		t.Fatalf("CreateAndPlan: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Run(ctx, &task); err != nil {
		t.Fatalf("Run: %v", err)
	}

	final, _ := store.Get(task.ID)
	if strings.Contains(final.Summary, "<|") {
		t.Fatalf("summary still contains raw special tokens: %q", final.Summary)
	}
	if !strings.Contains(final.Summary, "2 CPU cores") {
		t.Fatalf("summary lost the real content while sanitizing: %q", final.Summary)
	}
}

// TestEinoRunner_Run_RecoversEmbeddedFinishJSONWithoutPipeTokens locks in the
// SECOND leak shape observed live: gpt-oss-20b via LM Studio sometimes emits
// its channel-routing preamble WITHOUT the "<|...|>" pipe delimiters at all
// ("final to=functions.finishjson{...}" as bare text) — a shape
// SanitizeAgentText's token stripper cannot recognize since there's no
// delimiter to match. tryParseEmbeddedFinishJSON must recover the real
// summary/findings from the embedded JSON object regardless.
func TestEinoRunner_Run_RecoversEmbeddedFinishJSONWithoutPipeTokens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "1", "object": "chat.completion", "model": "test",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role": "assistant",
					"content": `final to=functions.finishjson{"findings":["Current user is pentester"],` +
						`"summary":"The sandbox runs under the user pentester and has two CPU cores available."}`,
				},
				"finish_reason": "stop",
			}},
		})
	}))
	defer srv.Close()

	store, _ := agent.NewStore(nil)
	r := NewEinoRunner(store, nil, nil, Config{
		ModelBaseURL: srv.URL + "/v1", ModelName: "test", ApprovalMode: agent.ModeAuto,
	})
	task, err := r.CreateAndPlan(context.Background(), "session-1", "who are we and how many cores")
	if err != nil {
		t.Fatalf("CreateAndPlan: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Run(ctx, &task); err != nil {
		t.Fatalf("Run: %v", err)
	}

	final, _ := store.Get(task.ID)
	if final.Summary != "The sandbox runs under the user pentester and has two CPU cores available." {
		t.Fatalf("summary not cleanly recovered from embedded JSON: %q", final.Summary)
	}
	if len(final.Findings) != 1 || final.Findings[0] != "Current user is pentester" {
		t.Fatalf("findings not cleanly recovered from embedded JSON: %v", final.Findings)
	}
}

// TestEinoRunner_Run_NudgesWhenModelStopsWithoutCallingFinish is the
// regression test for the gap found live against gpt-oss-20b: a model can
// end its ReAct turn WITHOUT ever calling the finish tool — just a plain
// assistant message and no more tool calls — which the original guard
// (gated on capture.called) completely missed, since nothing marked the
// model as "believing it's done". A multi-part goal answered only 1/2 ways
// this way must still get bounced once.
func TestEinoRunner_Run_NudgesWhenModelStopsWithoutCallingFinish(t *testing.T) {
	var turn int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		turn++
		w.Header().Set("Content-Type", "application/json")
		if turn == 1 {
			// One tool call (covers part 1), then a plain "I'm done" message
			// with no finish tool call at all — the exact gap this test locks.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "1", "object": "chat.completion", "model": "test",
				"choices": []map[string]any{{"index": 0, "message": map[string]any{
					"role": "assistant", "content": "",
					"tool_calls": []map[string]any{{
						"id": "call_1", "type": "function",
						"function": map[string]any{"name": "run_command", "arguments": `{"command":"whoami"}`},
					}},
				}, "finish_reason": "tool_calls"}},
			})
			return
		}
		if turn == 2 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "1", "object": "chat.completion", "model": "test",
				"choices": []map[string]any{{"index": 0, "message": map[string]any{
					"role": "assistant", "content": "The user is pentester.",
				}, "finish_reason": "stop"}},
			})
			return
		}
		// After the nudge's re-run: cover part 2 for real, then finish properly.
		if turn == 3 {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "1", "object": "chat.completion", "model": "test",
				"choices": []map[string]any{{"index": 0, "message": map[string]any{
					"role": "assistant", "content": "",
					"tool_calls": []map[string]any{{
						"id": "call_2", "type": "function",
						"function": map[string]any{"name": "run_command", "arguments": `{"command":"nproc"}`},
					}},
				}, "finish_reason": "tool_calls"}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "1", "object": "chat.completion", "model": "test",
			"choices": []map[string]any{{"index": 0, "message": map[string]any{
				"role": "assistant", "content": "",
				"tool_calls": []map[string]any{{
					"id": "call_3", "type": "function",
					"function": map[string]any{"name": "finish", "arguments": `{"summary":"user is pentester, 2 cores","findings":[]}`},
				}},
			}, "finish_reason": "tool_calls"}},
		})
	}))
	defer srv.Close()

	store, err := agent.NewStore(openTestDB(t))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	exec := &fakeExecer{ran: make(chan string, 4), content: "pentester"}
	r := NewEinoRunner(store, nil, exec, Config{
		ModelBaseURL: srv.URL + "/v1", ModelName: "test", ApprovalMode: agent.ModeAuto,
		PlanLLM: scriptedOllama(t, "check who we are\nfind out how many CPU cores"),
	})
	task, err := r.CreateAndPlan(context.Background(), "session-1", "check who we are and also find out how many CPU cores we have")
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

	nudged := false
	for _, ev := range store.ListActivity(task.SessionID, 50) {
		if strings.Contains(ev.Summary, "goal parts covered") {
			nudged = true
		}
	}
	if !nudged {
		t.Fatalf("expected the nudge to fire when the model stopped without calling finish, but it never did")
	}
}

// TestEinoRunner_Run_RetriesTransientToolCallError proves the bounded retry:
// a "error parsing tool call: ... unexpected end of JSON input" failure
// (live-observed against Ollama-served gpt-oss:20b — a truncated tool-call
// arguments JSON) must NOT fail the task outright. The first attempt errors,
// the second (identical setup) succeeds, and the task must end up completed.
func TestEinoRunner_Run_RetriesTransientToolCallError(t *testing.T) {
	var attempt int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt++
		if attempt == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`error parsing tool call: raw='{"command":"ls -l /var/spool` +
				`","rationale":"check","risk', err=unexpected end of JSON input`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "1", "object": "chat.completion", "model": "test",
			"choices": []map[string]any{{"index": 0, "message": map[string]any{
				"role": "assistant", "content": "",
				"tool_calls": []map[string]any{{
					"id": "call_1", "type": "function",
					"function": map[string]any{"name": "finish", "arguments": `{"summary":"recovered","findings":[]}`},
				}},
			}, "finish_reason": "tool_calls"}},
		})
	}))
	defer srv.Close()

	store, err := agent.NewStore(openTestDB(t))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	r := NewEinoRunner(store, nil, nil, Config{
		ModelBaseURL: srv.URL + "/v1", ModelName: "test", ApprovalMode: agent.ModeAuto,
	})
	task, err := r.CreateAndPlan(context.Background(), "session-1", "check the spool directory")
	if err != nil {
		t.Fatalf("CreateAndPlan: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Run(ctx, &task); err != nil {
		t.Fatalf("Run: %v (expected the transient error to be retried and recovered)", err)
	}

	final, _ := store.Get(task.ID)
	if final.Status != agent.StatusCompleted {
		t.Fatalf("status = %s, want completed", final.Status)
	}
	if final.Summary != "recovered" {
		t.Fatalf("summary = %q, want %q", final.Summary, "recovered")
	}
	if attempt != 2 {
		t.Fatalf("expected exactly 2 attempts (1 fail + 1 retry), got %d", attempt)
	}
}

// TestEinoRunner_Run_GivesUpAfterRepeatedTransientToolCallErrors proves the
// retry is bounded: a persistent (not one-off) tool-call error must still
// fail the task rather than retry forever.
func TestEinoRunner_Run_GivesUpAfterRepeatedTransientToolCallErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`error parsing tool call: raw='{"broken` + `', err=unexpected end of JSON input`))
	}))
	defer srv.Close()

	store, err := agent.NewStore(openTestDB(t))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	r := NewEinoRunner(store, nil, nil, Config{
		ModelBaseURL: srv.URL + "/v1", ModelName: "test", ApprovalMode: agent.ModeAuto,
	})
	task, err := r.CreateAndPlan(context.Background(), "session-1", "check the spool directory")
	if err != nil {
		t.Fatalf("CreateAndPlan: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Run(ctx, &task); err == nil {
		t.Fatal("expected Run to fail after exhausting the retry budget, got nil error")
	}

	final, _ := store.Get(task.ID)
	if final.Status != agent.StatusFailed {
		t.Fatalf("status = %s, want failed", final.Status)
	}
}

// TestEinoRunner_Run_StepsUsedMatchesRealChatModelTurns is the replacement
// coverage for the old per-tool-call StepsUsed test (moved out of
// runToolPipeline — see TestPentestTool_InvokeDoesNotItselfIncrementStepsUsed).
// Live-observed bug: steps_used was only bumped inside runToolPipeline, so a
// turn whose tool call never reached that pipeline still consumed a real
// Eino ReAct iteration invisibly — an operator saw e.g. "9/15" while Eino's
// own MaxIterations had already hit 15 and failed the task, with no way to
// see the real ceiling approaching. This script has THREE real ChatModel
// turns (two tool calls + finish), all reaching runToolPipeline normally, so
// StepsUsed must land on exactly 3 — proving the new choke point (Run()'s
// own event loop) counts real turns accurately, not just successfully-
// dispatched tool calls.
func TestEinoRunner_Run_StepsUsedMatchesRealChatModelTurns(t *testing.T) {
	var turn int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		turn++
		var toolCall map[string]any
		switch turn {
		case 1:
			toolCall = map[string]any{
				"id": "call_1", "type": "function",
				"function": map[string]any{"name": "run_command", "arguments": `{"command":"whoami"}`},
			}
		case 2:
			toolCall = map[string]any{
				"id": "call_2", "type": "function",
				"function": map[string]any{"name": "run_command", "arguments": `{"command":"nproc"}`},
			}
		default:
			toolCall = map[string]any{
				"id": "call_3", "type": "function",
				"function": map[string]any{"name": "finish", "arguments": `{"summary":"done","findings":[]}`},
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "1", "object": "chat.completion", "model": "test",
			"choices": []map[string]any{{"index": 0, "message": map[string]any{
				"role": "assistant", "content": "", "tool_calls": []map[string]any{toolCall},
			}, "finish_reason": "tool_calls"}},
		})
	}))
	defer srv.Close()

	store, err := agent.NewStore(openTestDB(t))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	exec := &fakeExecer{ran: make(chan string, 4), content: "ok"}
	r := NewEinoRunner(store, nil, exec, Config{
		ModelBaseURL: srv.URL + "/v1", ModelName: "test", ApprovalMode: agent.ModeAuto,
	})
	task, err := r.CreateAndPlan(context.Background(), "session-1", "check who we are and how many cores")
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
	if final.StepsUsed != 3 {
		t.Fatalf("StepsUsed = %d, want 3 (one per real ChatModel turn: 2 tool calls + finish)", final.StepsUsed)
	}
}

// TestEinoRunner_Run_ScalesStepBudgetForMultiPartGoals is the regression
// test for the fix that followed three separate LIVE runs against a real
// target with real cataloged pentest tools: a multi-part goal repeatedly
// exhausted the flat 15-step default — once with only TWO requested parts —
// because real-world tool friction (a missing wordlist) alone consumed
// nearly the whole budget before the goal's second part was ever reached.
// Configured StepBudget is deliberately tiny (2) here so that WITHOUT
// scaling this task would fail at iteration 2 — with a 4-part enumerated
// goal, scaling must give it enough real room (6 + 12*4 = 54) to finish a
// script that takes 6 real iterations.
func TestEinoRunner_Run_ScalesStepBudgetForMultiPartGoals(t *testing.T) {
	var turn int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		turn++
		var toolCall map[string]any
		if turn <= 5 {
			toolCall = map[string]any{
				"id": "call_r", "type": "function",
				"function": map[string]any{"name": "remember", "arguments": `{"key":"k","value":"v"}`},
			}
		} else {
			toolCall = map[string]any{
				"id": "call_f", "type": "function",
				"function": map[string]any{"name": "finish", "arguments": `{"summary":"all four parts done","findings":[]}`},
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "1", "object": "chat.completion", "model": "test",
			"choices": []map[string]any{{"index": 0, "message": map[string]any{
				"role": "assistant", "content": "", "tool_calls": []map[string]any{toolCall},
			}, "finish_reason": "tool_calls"}},
		})
	}))
	defer srv.Close()

	store, err := agent.NewStore(openTestDB(t))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	r := NewEinoRunner(store, nil, nil, Config{
		StepBudget: 2, ModelBaseURL: srv.URL + "/v1", ModelName: "test", ApprovalMode: agent.ModeAuto,
	})
	goal := "1) do part one 2) do part two 3) do part three 4) do part four"
	task, err := r.CreateAndPlan(context.Background(), "session-1", goal)
	if err != nil {
		t.Fatalf("CreateAndPlan: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Run(ctx, &task); err != nil {
		t.Fatalf("Run: %v (a 4-part goal should have been scaled well past the configured StepBudget of 2)", err)
	}

	final, _ := store.Get(task.ID)
	if final.Status != agent.StatusCompleted {
		t.Fatalf("status = %s, want completed", final.Status)
	}
	if final.StepBudget <= 2 {
		t.Fatalf("StepBudget = %d, want it scaled up from the configured 2 (operator-visible ceiling must reflect what was actually enforced)", final.StepBudget)
	}
}

// TestEinoRunner_Run_DoesNotScaleSingleIntentGoals proves the negative case:
// a goal with no detected multiple parts must NOT have its budget inflated —
// scaling only when there's a real, evidenced reason to expect more work is
// what keeps MaxIterations meaningful as a runaway-loop backstop.
func TestEinoRunner_Run_DoesNotScaleSingleIntentGoals(t *testing.T) {
	srv := toolCallServer("remember", `{"key":"k","value":"v"}`)
	defer srv.Close()

	store, _ := agent.NewStore(nil)
	r := NewEinoRunner(store, nil, nil, Config{
		StepBudget: 2, ModelBaseURL: srv.URL + "/v1", ModelName: "test", ApprovalMode: agent.ModeAuto,
	})
	task, err := r.CreateAndPlan(context.Background(), "session-1", "find the largest file under /var")
	if err != nil {
		t.Fatalf("CreateAndPlan: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Run(ctx, &task); err == nil {
		t.Fatal("expected the unscaled 2-step budget to still be enforced for a single-intent goal, got nil error")
	}
	final, _ := store.Get(task.ID)
	if final.StepBudget != 2 {
		t.Fatalf("StepBudget = %d, want unchanged at the configured 2 for a single-intent goal", final.StepBudget)
	}
}
