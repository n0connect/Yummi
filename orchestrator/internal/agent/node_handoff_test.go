package agent

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/n0connect/yummi/orchestrator/internal/conductor"
	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
)

// TestHandoffDelegatesToSubAgent proves the full Spec-3 mechanism end-to-end
// with REAL components (shared Store + Knowledge): the orchestrator emits
// [HANDOFF], the sub-agent runs its own graph, and its finding lands in the
// SHARED knowledge store under the same session — the whole point of building
// sub-agents on the unified world model.
func TestHandoffDelegatesToSubAgent(t *testing.T) {
	store, _ := NewStore(openTestDB(t))
	ks, _ := knowledge.NewStore(nil)
	execer := NewStoreExecer(realShellStore(t), 5*time.Second)

	// Sub-agent: plans one step, then finishes with a finding.
	subOllama := newFakeOllama(t,
		`{"steps":[{"id":"s1","description":"assess the web service"}]}`,
		`[FINISH]
{"summary":"web assessed","findings":["server exposes version banner"]}`,
	)
	subCfg := fastRetryCfg()
	subCfg.Knowledge = ks // shares the orchestrator's world model
	subRunner := NewRunner(store, subOllama, nil, execer, subCfg)

	// Orchestrator: plans, hands off to "web", then finishes.
	orchOllama := newFakeOllama(t,
		`{"steps":[{"id":"s1","description":"delegate web assessment"}]}`,
		`[HANDOFF]
{"role":"web","subgoal":"assess the web service at the target"}`,
		`[FINISH]
{"summary":"engagement complete","findings":[]}`,
	)
	orchCfg := fastRetryCfg()
	orchCfg.Knowledge = ks
	orchCfg.SubAgents = map[string]*Runner{"web": subRunner}
	orch := NewRunner(store, orchOllama, nil, execer, orchCfg)

	task, err := orch.CreateAndPlan(context.Background(), "sess1", "assess the web target")
	if err != nil {
		t.Fatalf("CreateAndPlan: %v", err)
	}
	if err := orch.Run(context.Background(), &task); err != nil {
		t.Fatalf("Run: %v", err)
	}

	final, _ := store.Get(task.ID)
	if final.Status != StatusCompleted {
		t.Fatalf("orchestrator task not completed: %s (lastErr=%q)", final.Status, final.LastError)
	}

	// The sub-agent's finding must be in the SHARED knowledge store, under the
	// orchestrator's session, attributed to an agent.
	found := false
	for _, f := range ks.ListFindings("sess1") {
		if strings.Contains(f.Title, "version banner") && f.Source == "agent" {
			found = true
		}
	}
	if !found {
		t.Fatalf("sub-agent finding did not reach shared knowledge: %+v", ks.ListFindings("sess1"))
	}
}

// Handoffs() must surface the configured sub-agents (was hard-nil before).
func TestHandoffsExposesSubAgents(t *testing.T) {
	store, _ := NewStore(openTestDB(t))
	sub := NewRunner(store, nil, nil, nil, fastRetryCfg())
	cfg := fastRetryCfg()
	cfg.SubAgents = map[string]*Runner{"web": sub}
	orch := NewRunner(store, nil, nil, nil, cfg)

	hs := orch.Handoffs()
	if len(hs) != 1 || hs["web"] == nil {
		t.Fatalf("Handoffs() did not expose sub-agents: %v", hs)
	}
	// A plain runner (no sub-agents) still returns nil.
	if NewRunner(store, nil, nil, nil, fastRetryCfg()).Handoffs() != nil {
		t.Fatal("runner without sub-agents should return nil Handoffs")
	}
}

func TestHandoffEmitsNestedActivity(t *testing.T) {
	store, _ := NewStore(openTestDB(t))
	ks, _ := knowledge.NewStore(nil)
	execer := NewStoreExecer(realShellStore(t), 5*time.Second)
	subOllama := newFakeOllama(t,
		`{"steps":[{"id":"s1","description":"sub"}]}`,
		`[FINISH]
{"summary":"sub done","findings":["x"]}`,
	)
	subCfg := fastRetryCfg()
	subCfg.Knowledge = ks
	sub := NewRunner(store, subOllama, nil, execer, subCfg)
	orchOllama := newFakeOllama(t,
		`{"steps":[{"id":"s1","description":"delegate"}]}`,
		`[HANDOFF]
{"role":"web","subgoal":"do sub"}`,
		`[FINISH]
{"summary":"done"}`,
	)
	orchCfg := fastRetryCfg()
	orchCfg.Knowledge = ks
	orchCfg.SubAgents = map[string]*Runner{"web": sub}
	orch := NewRunner(store, orchOllama, nil, execer, orchCfg)
	task, _ := orch.CreateAndPlan(context.Background(), "s1", "go")
	if err := orch.Run(context.Background(), &task); err != nil {
		t.Fatalf("run: %v", err)
	}
	var sawStart, sawNested bool
	for _, a := range store.ListActivity("s1", 200) {
		if a.Kind == ActHandoffIn {
			sawStart = true
		}
		if a.ParentTaskID == task.ID {
			sawNested = true
		}
	}
	if !sawStart || !sawNested {
		t.Fatalf("handoff nesting missing: start=%v nested=%v", sawStart, sawNested)
	}
}

// TestApproveResolvesSubAgentGate is a regression test for the security audit
// finding that a sub-agent's pending approval was structurally unreachable:
// each sub-agent runs its own task loop against its own private *Gate, but
// the only API path (HandleApproveTask -> the top-level Runner.Approve) used
// to call only that top runner's gate, so a delegated sub-agent's medium+
// risk command could never be approved — it would always time out. This
// proves the top-level runner's Approve now resolves a sub-agent's gate too.
func TestApproveResolvesSubAgentGate(t *testing.T) {
	store, _ := NewStore(openTestDB(t))
	ks, _ := knowledge.NewStore(nil)
	execer := NewStoreExecer(realShellStore(t), 5*time.Second)
	validator := conductor.NewValidator() // real RequiresApproval from risk_level

	subOllama := newFakeOllama(t,
		`{"steps":[{"id":"s1","description":"do a risky thing"}]}`,
		"[RUN_COMMAND]\n{\"command\":\"echo risky\",\"step_id\":\"s1\",\"risk_level\":\"medium\"}",
		`[FINISH]
{"summary":"sub done"}`,
	)
	subCfg := fastRetryCfg()
	subCfg.Knowledge = ks
	subCfg.ApprovalMode = ModeAskPermission
	sub := NewRunner(store, subOllama, validator, execer, subCfg)

	orchOllama := newFakeOllama(t,
		`{"steps":[{"id":"s1","description":"delegate"}]}`,
		`[HANDOFF]
{"role":"web","subgoal":"do the risky thing"}`,
		`[FINISH]
{"summary":"done"}`,
	)
	orchCfg := fastRetryCfg()
	orchCfg.Knowledge = ks
	orchCfg.SubAgents = map[string]*Runner{"web": sub}
	orch := NewRunner(store, orchOllama, nil, execer, orchCfg)

	sessionID := "sess-sub-approve"
	task, err := orch.CreateAndPlan(context.Background(), sessionID, "delegate risky work")
	if err != nil {
		t.Fatalf("CreateAndPlan: %v", err)
	}

	runErrCh := make(chan error, 1)
	go func() { runErrCh <- orch.Run(context.Background(), &task) }()

	// The sub-agent's own task (not the orchestrator's, which stays "running"
	// for the duration of the synchronous handoff) is what reaches
	// awaiting_approval — both share the same Store.
	var subTask Task
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, tk := range store.ListBySession(sessionID, 50) {
			if tk.Status == StatusAwaitingApproval {
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

	// The regression check: approving through the TOP-LEVEL runner (the only
	// one the API has a handle to via HandleApproveTask) must resolve the
	// sub-agent's gate, not just the orchestrator's own (empty) one.
	if err := orch.Approve(subTask.ID, subTask.PendingProposalID, true, ""); err != nil {
		t.Fatalf("orch.Approve on sub-agent task: %v", err)
	}

	if err := <-runErrCh; err != nil {
		t.Fatalf("orch.Run: %v", err)
	}
	final, _ := store.Get(task.ID)
	if final.Status != StatusCompleted {
		t.Fatalf("orchestrator task not completed: %s (lastErr=%q)", final.Status, final.LastError)
	}
	// The discoverability fix this test package exists to cover: the
	// sub-agent's task must carry the orchestrator's task ID as its
	// ParentTaskID, so the UI can surface its pending approval under the
	// parent it watches instead of it being an orphaned ID the operator never
	// learns about.
	if subTask.ParentTaskID != task.ID {
		t.Fatalf("sub-task ParentTaskID = %q, want orchestrator task ID %q", subTask.ParentTaskID, task.ID)
	}
}

// TestRunSubtaskParentTaskIDDoesNotRaceAcrossConcurrentHandoffs is a
// regression test: RunSubtask used to write the parent link to
// Config.ParentTaskID, a field shared by the whole *Runner — a sub-agent
// Runner instance is configured once and reused for every handoff, so two
// parent tasks delegating to the SAME role concurrently could have one
// task's sub-task end up tagged with the OTHER parent's ID, depending on
// goroutine timing. Threading parentTaskID straight into task creation
// (CreateSubtask) instead removes the shared mutable field entirely; this
// test would flake under the old implementation and must pass reliably now.
func TestRunSubtaskParentTaskIDDoesNotRaceAcrossConcurrentHandoffs(t *testing.T) {
	// A real on-disk file, not openTestDB's plain ":memory:" — under concurrent
	// writes the sql.DB pool opens multiple physical connections, and a bare
	// ":memory:" DSN hands each new connection its own empty database (no
	// shared cache), so schema creation on connection #1 wouldn't be visible
	// to connections #2+. This test is the only one in the package that
	// actually exercises concurrent writes, hence the dedicated setup.
	dbPath := filepath.Join(t.TempDir(), "agent.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, _ := NewStore(db)

	// RunSubtask's first act is exactly this call (CreateSubtask), now with
	// parentTaskID threaded straight in as a parameter — under the old
	// implementation this was instead a write to the shared Runner field
	// Config.ParentTaskID, read back later by a different goroutine, the
	// classic write-then-read-elsewhere shape of the race this test guards
	// against. Driving many concurrent calls against the same Store (as many
	// concurrent handoffs from different parent tasks would) must never let
	// one call's parent ID leak onto another's sub-task.
	const n = 20
	parentIDs := make([]string, n)
	for i := range parentIDs {
		parentIDs[i] = uuid.NewString()
	}

	var wg sync.WaitGroup
	errs := make([]error, n)
	subTaskIDs := make([]string, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			task, err := store.CreateSubtask("sess", "goal", 5, parentIDs[i])
			errs[i] = err
			subTaskIDs[i] = task.ID
		}()
	}
	wg.Wait()

	for i := range n {
		if errs[i] != nil {
			t.Fatalf("CreateSubtask[%d]: %v", i, errs[i])
		}
		got, ok := store.Get(subTaskIDs[i])
		if !ok {
			t.Fatalf("subtask %d not found", i)
		}
		if got.ParentTaskID != parentIDs[i] {
			t.Fatalf("subtask %d ParentTaskID = %q, want %q (cross-contamination)", i, got.ParentTaskID, parentIDs[i])
		}
	}
}

func TestHandoffActionParse(t *testing.T) {
	// Tagged form.
	msg, err := ParseControl(`[HANDOFF]
{"role":"web","subgoal":"assess the target"}`)
	if err != nil || msg.Tag != "HANDOFF" || msg.Action.Role != "web" || msg.Action.Subgoal != "assess the target" {
		t.Fatalf("tagged HANDOFF parse wrong: tag=%q action=%+v err=%v", msg.Tag, msg.Action, err)
	}
	// Tagless inference (weak models omit the tag).
	msg2, err := ParseControl(`{"role":"recon","subgoal":"enumerate ports"}`)
	if err != nil || msg2.Tag != "HANDOFF" || msg2.Action.Role != "recon" {
		t.Fatalf("tagless HANDOFF inference wrong: tag=%q action=%+v err=%v", msg2.Tag, msg2.Action, err)
	}
}

// The DELEGATION block is shown only when sub-agents exist, so a sub-agent
// (empty SubAgents) never sees the option — the structural depth-1 guard.
func TestDelegationBlockOnlyWithSubAgents(t *testing.T) {
	withSub := &GraphState{cfg: Config{SubAgents: map[string]*Runner{"web": {}, "recon": {}}}}
	block := withSub.buildDelegation()
	if !strings.Contains(block, "DELEGATION") || !strings.Contains(block, "web") || !strings.Contains(block, "recon") {
		t.Fatalf("delegation block missing roles: %q", block)
	}

	noSub := &GraphState{cfg: Config{}}
	if noSub.buildDelegation() != "" {
		t.Fatal("a sub-agent (no SubAgents) must not see the delegation option")
	}
}
