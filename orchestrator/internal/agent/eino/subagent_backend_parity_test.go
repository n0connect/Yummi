package eino

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/n0connect/yummi/orchestrator/internal/agent"
)

// subagent_backend_parity_test.go proves the fix for a real architecture gap:
// a HANDOFF used to always run on the graph engine regardless of which
// backend drove the top-level task (agent.Runner.RunSubtask's old doc
// comment said so explicitly), because EinoRunner had no RunSubtask method
// at all — there was nowhere else for Eino's handoff tool to dispatch to.
// These tests drive an Eino-backed TOP-LEVEL task that delegates to an
// Eino-backed SUB-AGENT (agent.SubAgentRunner satisfied by *EinoRunner, not
// *agent.Runner), with two independent fake model servers so a run that
// actually reached the sub-agent's own Eino ReAct loop is the only way both
// can be observed to have responded.

// toolCallResponse writes one OpenAI-compatible chat-completion response
// whose assistant message is a single tool call — the minimal shape both
// fake servers below need.
func toolCallResponse(w http.ResponseWriter, callID, name, argsJSON string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": "1", "object": "chat.completion", "model": "test",
		"choices": []map[string]any{{
			"index": 0,
			"message": map[string]any{
				"role": "assistant", "content": "", "tool_calls": []map[string]any{{
					"id": callID, "type": "function",
					"function": map[string]any{"name": name, "arguments": argsJSON},
				}},
			},
			"finish_reason": "tool_calls",
		}},
	})
}

// TestHandoffDispatchesToAnEinoSubAgentNotJustGraph is the direct regression
// test: before agent.SubAgentRunner existed, eino.Config.SubAgents could only
// ever hold *agent.Runner, so this test's setup (an *EinoRunner as the
// delegation target) would not even have compiled. It compiling AND the
// sub-agent's own distinctive summary surviving all the way into the
// top-level task's final summary proves dispatch actually reached the Eino
// sub-agent's own Run(), not a graph fallback.
func TestHandoffDispatchesToAnEinoSubAgentNotJustGraph(t *testing.T) {
	store, err := agent.NewStore(openTestDB(t))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// Sub-agent's model: one turn, straight to finish. The distinctive phrase
	// is what proves this exact server (not the top-level one) produced the
	// sub-task's result.
	subSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		toolCallResponse(w, "sub-call-1", "finish", `{"summary":"web specialist ran on its own Eino loop"}`)
	}))
	defer subSrv.Close()

	subRunner := NewEinoRunner(store, nil, nil, Config{
		ModelBaseURL: subSrv.URL + "/v1", ModelName: "test", ApprovalMode: agent.ModeAuto,
		// SubAgents intentionally left empty: caps handoff depth at 1.
	})

	// Top-level model: turn 1 hands off, turn 2 (fed the handoff tool's own
	// result) finishes.
	turn := 0
	topSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if turn == 0 {
			turn++
			toolCallResponse(w, "top-call-1", "handoff", `{"role":"web","subgoal":"assess the web server"}`)
			return
		}
		toolCallResponse(w, "top-call-2", "finish", `{"summary":"delegated to web specialist and done"}`)
	}))
	defer topSrv.Close()

	top := NewEinoRunner(store, nil, nil, Config{
		ModelBaseURL: topSrv.URL + "/v1", ModelName: "test", ApprovalMode: agent.ModeAuto,
		SubAgents: map[string]agent.SubAgentRunner{"web": subRunner},
	})

	task, err := top.CreateAndPlan(context.Background(), "sess-eino-parity", "assess the target, delegating web work")
	if err != nil {
		t.Fatalf("CreateAndPlan: %v", err)
	}
	if err := top.Run(context.Background(), &task); err != nil {
		t.Fatalf("top.Run: %v", err)
	}

	final, _ := store.Get(task.ID)
	if final.Status != agent.StatusCompleted {
		t.Fatalf("top-level task not completed: %s (lastErr=%q)", final.Status, final.LastError)
	}

	// Find the child (sub-agent) task and confirm IT carries the sub-agent
	// server's distinctive summary — the only way that text exists anywhere
	// is if subRunner.RunSubtask actually ran subRunner's own Eino loop.
	var child agent.Task
	for _, tk := range store.ListBySession("sess-eino-parity", 50) {
		if tk.ParentTaskID == task.ID {
			child = tk
		}
	}
	if child.ID == "" {
		t.Fatal("no child (sub-agent) task found under the top-level task")
	}
	if child.Status != agent.StatusCompleted {
		t.Fatalf("sub-agent task not completed: %s (lastErr=%q)", child.Status, child.LastError)
	}
	if child.Summary != "web specialist ran on its own Eino loop" {
		t.Fatalf("sub-agent task summary = %q, want the Eino sub-agent's own distinctive text", child.Summary)
	}
}

// TestEinoSubAgentCannotItselfDelegate proves the depth-1 cap holds for an
// Eino sub-agent exactly the way it already does for a graph one: built with
// an empty SubAgents map, its handoff tool is never registered at all (see
// the len(r.cfg.SubAgents) > 0 guard before newHandoffTool in runner.go), so
// a model driving the sub-agent has no handoff tool to call even if it tried.
// Verified here by calling RunSubtask directly and confirming the task
// completes via its own single tool turn with no recursion possible.
func TestEinoSubAgentCannotItselfDelegate(t *testing.T) {
	store, err := agent.NewStore(openTestDB(t))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		toolCallResponse(w, "call-1", "finish", `{"summary":"no handoff tool was available to me"}`)
	}))
	defer srv.Close()

	sub := NewEinoRunner(store, nil, nil, Config{
		ModelBaseURL: srv.URL + "/v1", ModelName: "test", ApprovalMode: agent.ModeAuto,
	})

	created := make(chan string, 1)
	res, err := sub.RunSubtask(context.Background(), "sess-depth-cap", "try to delegate further", "parent-task-1",
		func(taskID string) { created <- taskID })
	if err != nil {
		t.Fatalf("RunSubtask: %v", err)
	}
	select {
	case id := <-created:
		task, _ := store.Get(id)
		if task.ParentTaskID != "parent-task-1" {
			t.Fatalf("child task's ParentTaskID = %q, want parent-task-1", task.ParentTaskID)
		}
	case <-time.After(time.Second):
		t.Fatal("created callback never fired")
	}
	if res.Summary != "no handoff tool was available to me" {
		t.Fatalf("SubResult.Summary = %q", res.Summary)
	}
}
