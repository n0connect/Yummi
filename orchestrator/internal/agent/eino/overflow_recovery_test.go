package eino

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/n0connect/yummi/orchestrator/internal/agent"
)

// overflowThenFinish drives a real run through the real HTTP client:
// call 1 issues a run_command whose output must survive; call 2 is answered
// with the provider's actual context-overflow 400 body; call 3 finishes. The
// recovered segment must carry the earlier result forward in compacted form.
type overflowThenFinish struct {
	mu     sync.Mutex
	calls  int
	bodies []string
}

func (m *overflowThenFinish) handler(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	m.mu.Lock()
	m.calls++
	call := m.calls
	m.bodies = append(m.bodies, string(b))
	m.mu.Unlock()

	if call == 2 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"This model's maximum context length is 32768 tokens. However, you requested about 40112 tokens (39000 in the messages, 1112 in the completion).","code":"context_length_exceeded","type":"invalid_request_error"}}`)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	var msg map[string]any
	if call == 1 {
		msg = map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{
			"id": "call_run", "type": "function",
			"function": map[string]any{"name": "run_command", "arguments": `{"command":"echo prior-recon-XYZ","rationale":"gather facts","risk_level":"low"}`},
		}}}
	} else {
		msg = map[string]any{"role": "assistant", "content": "", "tool_calls": []map[string]any{{
			"id": "call_fin", "type": "function",
			"function": map[string]any{"name": "finish", "arguments": `{"summary":"recovered after compaction"}`},
		}}}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": "1", "object": "chat.completion", "model": "test",
		"choices": []map[string]any{{"index": 0, "message": msg, "finish_reason": "tool_calls"}},
	})
}

func TestContextOverflowRecoversIntoCompactedSegment(t *testing.T) {
	store, err := agent.NewStore(openTestDB(t))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	execer := agent.NewStoreExecer(realShellStore(t), 5*time.Second)
	model := &overflowThenFinish{}
	srv := httptest.NewServer(http.HandlerFunc(model.handler))
	defer srv.Close()

	r := NewEinoRunner(store, nil, execer, Config{
		ModelBaseURL: srv.URL + "/v1", ModelName: "test", ApprovalMode: agent.ModeAuto, StepBudget: 15,
	})
	task, err := r.CreateAndPlan(context.Background(), "sess-overflow", "gather facts, then finish")
	if err != nil {
		t.Fatalf("CreateAndPlan: %v", err)
	}
	if err := r.Run(context.Background(), &task); err != nil {
		t.Fatalf("Run: %v", err)
	}

	final, _ := store.Get(task.ID)
	if final.Status != agent.StatusCompleted {
		t.Fatalf("overflow must be recovered, not fail the task: status=%s lastErr=%q", final.Status, final.LastError)
	}
	model.mu.Lock()
	bodies := append([]string(nil), model.bodies...)
	model.mu.Unlock()
	if len(bodies) < 3 {
		t.Fatalf("want a recovered third model call, got %d", len(bodies))
	}
	recovered := bodies[len(bodies)-1]
	if !strings.Contains(recovered, "COMPACTED PRIOR WORK") {
		t.Fatalf("the recovered segment must open with the compacted prior work")
	}
	if !strings.Contains(recovered, "prior-recon-XYZ") {
		t.Fatalf("the earlier result must survive the overflow, not be dropped")
	}
	var sawRecovery bool
	for _, a := range store.ListActivity("sess-overflow", 100) {
		if strings.Contains(a.Summary, "context window full") {
			sawRecovery = true
		}
	}
	if !sawRecovery {
		t.Fatal("the recovery must be visible in TRACE, not silent")
	}
}

// The carry-forward must stay bounded: a long task's work log keeps the early
// topology and the recent tail, states what it omitted, and cuts each result.
func TestBoundedWorkLogKeepsHeadAndTailAndCountsOmitted(t *testing.T) {
	var pairs []tracePair
	for i := range 40 {
		pairs = append(pairs, tracePair{Tool: "nmap", Result: strings.Repeat("x", 2000) + "end" + string(rune('A'+i%26))})
	}
	log := boundedWorkLog(pairs)
	if !strings.Contains(log, "middle tool calls omitted for length") {
		t.Fatal("the omitted middle must be stated")
	}
	if n := strings.Count(log, "\n"); n > workLogHeadPairs+workLogTailPairs+2 {
		t.Fatalf("work log has %d lines, want bounded to about %d", n, workLogHeadPairs+workLogTailPairs+2)
	}
	if strings.Count(log, "xxxx") > 40*workLogResultRunes {
		t.Fatal("each result must be cut to the rune bound")
	}
}
