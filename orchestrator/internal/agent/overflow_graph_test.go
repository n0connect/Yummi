package agent

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/n0connect/yummi/orchestrator/internal/llm"
)

// overflowOllama is a real HTTP server standing in for Ollama's native chat API
// and answering every request with the provider's actual overflow phrasing.
// stepNode's model call goes through the real llm.Client over real HTTP, so the
// error it returns is the same shape production sees.
func overflowOllama(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"the input length exceeds the context length"}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func overflowStepState(t *testing.T, observations []string, srvURL string) *GraphState {
	t.Helper()
	store, err := NewStore(openTestDB(t))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	task, _ := store.Create("overflow", "probe the target", 10)
	_ = store.Transition(task.ID, StatusPlanned, nil)
	_ = store.Transition(task.ID, StatusRunning, nil)
	return &GraphState{
		taskID: task.ID, store: store, cfg: fastRetryCfg(),
		memCtx: MemoryContext{TokenBudget: 6000, Observations: observations},
		llm:    llm.NewClient(srvURL, "test-model"),
	}
}

// A context overflow with prior observations must force compaction, not a
// retry: re-sending the same oversized prompt can never succeed.
func TestStepNodeContextOverflowForcesCompaction(t *testing.T) {
	srv := overflowOllama(t)
	s := overflowStepState(t, []string{"22/tcp open ssh", "80/tcp open http"}, srv.URL)

	edge, err := stepNode(context.Background(), s)
	if err != nil {
		t.Fatalf("stepNode: %v", err)
	}
	if edge != edgeContextFull {
		t.Fatalf("overflow must route to compaction (%s), got %q", edgeContextFull, edge)
	}
	if s.memCtx.TokensUsed != s.memCtx.TokenBudget {
		t.Fatalf("compaction must be forced by saturating TokensUsed, got %d of %d", s.memCtx.TokensUsed, s.memCtx.TokenBudget)
	}
	if s.overflowCompactions != 1 {
		t.Fatalf("overflowCompactions = %d, want 1", s.overflowCompactions)
	}
}

// With nothing to compact, an overflow cannot be cured by compaction; it must
// take the normal retry path rather than a compaction that does nothing.
func TestStepNodeContextOverflowWithoutObservationsStaysOnRetryPath(t *testing.T) {
	srv := overflowOllama(t)
	s := overflowStepState(t, nil, srv.URL)

	edge, err := stepNode(context.Background(), s)
	if err != nil {
		t.Fatalf("stepNode: %v", err)
	}
	if edge != edgeLLMError {
		t.Fatalf("nothing to compact: want the LLM-error path (%s), got %q", edgeLLMError, edge)
	}
	if s.overflowCompactions != 0 {
		t.Fatalf("no compaction should have been counted, got %d", s.overflowCompactions)
	}
	if !strings.Contains(s.lastErr, "context window exhausted") {
		t.Fatalf("an overflow with nothing to compact must still name the cause, got lastErr=%q", s.lastErr)
	}
}

// Compaction is bounded per task: once the cap is reached, an overflow falls
// through to the normal retry/give-up path instead of looping forever.
func TestStepNodeContextOverflowCompactionIsBounded(t *testing.T) {
	srv := overflowOllama(t)
	s := overflowStepState(t, []string{"22/tcp open ssh"}, srv.URL)
	s.overflowCompactions = maxOverflowCompactions

	edge, err := stepNode(context.Background(), s)
	if err != nil {
		t.Fatalf("stepNode: %v", err)
	}
	if edge != edgeLLMError {
		t.Fatalf("at the compaction cap the overflow must stop compacting (%s), got %q", edgeLLMError, edge)
	}
}
