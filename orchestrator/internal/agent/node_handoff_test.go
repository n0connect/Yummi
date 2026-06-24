package agent

import (
	"context"
	"strings"
	"testing"
	"time"

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
