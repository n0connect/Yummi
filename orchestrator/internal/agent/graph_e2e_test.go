package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/n0connect/yummi/orchestrator/internal/execution"
	"github.com/n0connect/yummi/orchestrator/internal/llm"
)

// graphScript answers Ollama's native /api/chat for a whole graph run. Each
// request is routed by its content, not by call order, so the planning call,
// the goal-part audit, the compaction call and the step calls cannot be
// confused with one another. Step calls are served from `steps` in order; a
// step whose entry is "OVERFLOW" gets the provider's real overflow reply.
type graphScript struct {
	mu     sync.Mutex
	plan   string // planning reply; default is a two-step plan
	steps  []string
	next   int
	bodies []string // every request body, in order
}

func (g *graphScript) handler(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	body := string(b)
	g.mu.Lock()
	g.bodies = append(g.bodies, body)
	var (
		content string
		status  = http.StatusOK
	)
	switch {
	case strings.Contains(body, "OUTPUT: reply with ONLY this JSON"): // planning
		content = g.plan
		if content == "" {
			content = `{"steps":[{"id":"s1","description":"run a probe"},{"id":"s2","description":"report what was found"}]}`
		}
	case strings.Contains(body, "You audit operator goals"): // goal-part audit: one line = one request
		content = "probe the target"
	case strings.Contains(body, "Summarize prior tool observations"): // compaction
		content = "CONSOLIDATED-SUMMARY: probe ran, prior-recon-XYZ observed."
	default: // a step
		step := "[FINISH]\n{\"summary\":\"done\"}"
		if g.next < len(g.steps) {
			step = g.steps[g.next]
			g.next++
		}
		if step == "OVERFLOW" {
			status = http.StatusBadRequest
			content = `{"error":"the input length exceeds the context length"}`
		} else {
			content = step
		}
	}
	g.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if status == http.StatusOK {
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"role": "assistant", "content": content}, "done": true})
		return
	}
	_, _ = io.WriteString(w, content)
}

func (g *graphScript) requests() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.bodies...)
}

func graphE2ERunner(t *testing.T, script *graphScript) (*Runner, *Store, string) {
	t.Helper()
	store, err := NewStore(openTestDB(t))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	es := execution.NewStore()
	es.SetProcessor(func(ctx context.Context, rec execution.Record) (string, error) {
		out, err := exec.CommandContext(ctx, "sh", "-c", rec.Command).CombinedOutput()
		return string(out), err
	})
	srv := httptest.NewServer(http.HandlerFunc(script.handler))
	t.Cleanup(srv.Close)
	r := NewRunner(store, llm.NewClient(srv.URL, "test-model"), nil, NewStoreExecer(es, 5*time.Second), Config{
		StepBudget: 8, ApprovalMode: ModeAuto, SoftTimeout: 5 * time.Second,
	})
	return r, store, srv.URL
}

// The graph engine runs a real command, feeds its result back to the model,
// and completes when the model finishes. This is the baseline the overflow
// test below builds on.
func TestGraphE2ERunsCommandFeedsResultBackAndCompletes(t *testing.T) {
	script := &graphScript{steps: []string{
		"[RUN_COMMAND]\n{\"command\":\"echo prior-recon-XYZ\",\"step_id\":\"s1\",\"rationale\":\"probe\",\"risk_level\":\"low\"}",
		"[FINISH]\n{\"summary\":\"done\"}",
	}}
	r, store, _ := graphE2ERunner(t, script)
	task, err := r.CreateAndPlan(context.Background(), "sess-e2e", "probe the target")
	if err != nil {
		t.Fatalf("CreateAndPlan: %v", err)
	}
	if err := r.Run(context.Background(), &task); err != nil {
		t.Fatalf("Run: %v", err)
	}
	final, _ := store.Get(task.ID)
	if final.Status != StatusCompleted {
		t.Fatalf("status = %s, want completed (lastErr=%q)", final.Status, final.LastError)
	}
	reqs := script.requests()
	var sawResult bool
	for _, b := range reqs {
		if strings.Contains(b, "prior-recon-XYZ") && strings.Contains(b, "TOOL_RESPONSE") {
			sawResult = true
		}
	}
	if !sawResult {
		t.Fatal("the command's real output must reach the model's next step as a TOOL_RESPONSE observation")
	}
}

// A context overflow in the middle of a real run is compacted and the task
// completes, with the next step built on the compaction summary.
func TestGraphE2EContextOverflowCompactsAndCompletes(t *testing.T) {
	script := &graphScript{steps: []string{
		"[RUN_COMMAND]\n{\"command\":\"echo prior-recon-XYZ\",\"step_id\":\"s1\",\"rationale\":\"probe\",\"risk_level\":\"low\"}",
		"OVERFLOW",
		"[FINISH]\n{\"summary\":\"recovered after compaction\"}",
	}}
	r, store, _ := graphE2ERunner(t, script)
	task, err := r.CreateAndPlan(context.Background(), "sess-e2e-overflow", "probe the target")
	if err != nil {
		t.Fatalf("CreateAndPlan: %v", err)
	}
	if err := r.Run(context.Background(), &task); err != nil {
		t.Fatalf("Run: %v", err)
	}
	final, _ := store.Get(task.ID)
	if final.Status != StatusCompleted {
		t.Fatalf("overflow must be compacted and survived: status=%s lastErr=%q", final.Status, final.LastError)
	}
	reqs := script.requests()
	last := reqs[len(reqs)-1]
	if !strings.Contains(last, "CONSOLIDATED-SUMMARY") {
		t.Fatal("the step after the overflow must be built on the compaction summary")
	}
}

// On a single-step plan that is already complete, the graph converges to a
// deterministic finish instead of re-asking the model. An overflow arriving
// at that point must take that convergence (no compaction loop, no hang).
func TestGraphE2EOverflowOnCompletedPlanConvergesWithoutLooping(t *testing.T) {
	script := &graphScript{
		plan: `{"steps":[{"id":"s1","description":"run a probe"}]}`,
		steps: []string{
			"[RUN_COMMAND]\n{\"command\":\"echo prior-recon-XYZ\",\"step_id\":\"s1\",\"rationale\":\"probe\",\"risk_level\":\"low\"}",
			"OVERFLOW",
		},
	}
	r, store, _ := graphE2ERunner(t, script)
	task, err := r.CreateAndPlan(context.Background(), "sess-e2e-terminal", "probe the target")
	if err != nil {
		t.Fatalf("CreateAndPlan: %v", err)
	}
	if err := r.Run(context.Background(), &task); err != nil {
		t.Fatalf("Run: %v", err)
	}
	final, _ := store.Get(task.ID)
	if final.Status != StatusCompleted {
		t.Fatalf("a completed plan must converge to finish on overflow: status=%s lastErr=%q", final.Status, final.LastError)
	}
	if n := len(script.requests()); n > 4 {
		t.Fatalf("overflow must not trigger a retry/compaction loop: %d model requests", n)
	}
}
