package eino

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/n0connect/yummi/orchestrator/internal/agent"
	"github.com/n0connect/yummi/orchestrator/internal/execution"
	"github.com/n0connect/yummi/orchestrator/internal/llm"
)

// openTestDB mirrors package agent's own test helper of the same name (that
// one is unexported and lives in a different package): a real SQLite
// connection is required here because Store.ListActivity/RecordActivity are
// no-ops against the nil-db, in-memory-only mode agent.NewStore(nil) gives
// the rest of this package's tests.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// scriptedOllama serves a fixed sequence of canned replies against Ollama's
// native /api/chat response shape ({"message":{...},"done":true}) — the
// format agent.Runner's llm.Client expects, distinct from the OpenAI-shaped
// endpoint EinoRunner's own ChatModel talks to. This is what lets this
// package build a REAL graph sub-agent (agent.Runner) for the handoff tool
// to delegate to, matching the project's own node_handoff_test.go pattern —
// not a mock of Yummi's own code, just a scripted stand-in for Ollama itself.
func scriptedOllama(t *testing.T, replies ...string) *llm.Client {
	t.Helper()
	var mu sync.Mutex
	idx := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		content := replies[idx]
		if idx < len(replies)-1 {
			idx++
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		contentJSON, _ := json.Marshal(content)
		fmt.Fprintf(w, `{"message":{"role":"assistant","content":%s},"done":true}`, contentJSON)
	}))
	t.Cleanup(srv.Close)
	return llm.NewClient(srv.URL, "test-model")
}

func newGraphSubAgent(t *testing.T, store *agent.Store, execer agent.Execer, replies ...string) *agent.Runner {
	t.Helper()
	cfg := agent.Config{StepBudget: 5, Retry: agent.RetryConfig{MaxAttempts: 1}}
	return agent.NewRunner(store, scriptedOllama(t, replies...), nil, execer, cfg)
}

func TestHandoffTool_DelegatesToSubAgentAndReturnsSummary(t *testing.T) {
	store, err := agent.NewStore(openTestDB(t))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	execer := agent.NewStoreExecer(execution.NewStore(), 5*time.Second)

	sub := newGraphSubAgent(t, store, execer,
		`{"steps":[{"id":"s1","description":"assess"}]}`,
		`[FINISH]
{"summary":"web assessed, nothing critical","findings":["server: nginx 1.18"]}`,
	)

	task, err := store.CreateSubtask("sess-1", "top-level goal", 5, "")
	if err != nil {
		t.Fatalf("CreateSubtask: %v", err)
	}

	ht := newHandoffTool(handoffDeps{
		taskID: task.ID, sessionID: "sess-1", store: store,
		subAgents:   map[string]*agent.Runner{"web": sub},
		delegations: agent.NewDelegationCoordinator(3),
		timeout:     5 * time.Second,
	})

	out, err := ht.InvokableRun(context.Background(), `{"role":"web","subgoal":"assess the web service"}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if !strings.Contains(out, "web assessed") || !strings.Contains(out, "nginx 1.18") {
		t.Fatalf("handoff output missing sub-agent summary/findings: %q", out)
	}

	var sawIn, sawOut bool
	for _, a := range store.ListActivity("sess-1", 50) {
		if a.Kind == agent.ActHandoffIn {
			sawIn = true
		}
		if a.Kind == agent.ActHandoffOut {
			sawOut = true
		}
	}
	if !sawIn || !sawOut {
		t.Fatalf("expected both ActHandoffIn and ActHandoffOut activity, in=%v out=%v", sawIn, sawOut)
	}
}

func TestHandoffTool_UnknownRoleRejected(t *testing.T) {
	store, _ := agent.NewStore(nil)
	task, _ := store.CreateSubtask("sess-1", "goal", 5, "")
	ht := newHandoffTool(handoffDeps{
		taskID: task.ID, sessionID: "sess-1", store: store,
		subAgents:   map[string]*agent.Runner{"web": {}},
		delegations: agent.NewDelegationCoordinator(3),
		timeout:     5 * time.Second,
	})

	out, err := ht.InvokableRun(context.Background(), `{"role":"ghost","subgoal":"do something"}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	if !strings.Contains(out, "unknown role") {
		t.Fatalf("expected unknown-role rejection, got %q", out)
	}
}

func TestHandoffInfo_ListsConfiguredRoles(t *testing.T) {
	ht := newHandoffTool(handoffDeps{subAgents: map[string]*agent.Runner{"web": {}, "recon": {}}})
	info, err := ht.Info(context.Background())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if !strings.Contains(info.Desc, "web") || !strings.Contains(info.Desc, "recon") {
		t.Fatalf("tool description missing configured roles: %q", info.Desc)
	}
}
