package eino

import (
	"context"
	"strings"
	"testing"

	"github.com/n0connect/yummi/orchestrator/internal/agent"
	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
	"github.com/n0connect/yummi/orchestrator/internal/skill"
)

// fakeRetrieval is a plain recording double for knowledge.RetrievalStrategy —
// a small interface owned by this codebase, matching the no-mocks convention.
type fakeRetrieval struct {
	items []knowledge.MemoryItem
	err   error
	gotQ  knowledge.Query
}

func (f *fakeRetrieval) Retrieve(_ context.Context, q knowledge.Query) ([]knowledge.MemoryItem, error) {
	f.gotQ = q
	return f.items, f.err
}

func TestBuildMemoryBlock_NilRetrieval(t *testing.T) {
	if got := buildMemoryBlock(context.Background(), nil, "sess", "goal"); got != "" {
		t.Fatalf("nil retrieval should yield empty block, got %q", got)
	}
}

func TestBuildMemoryBlock_RendersItems(t *testing.T) {
	fr := &fakeRetrieval{items: []knowledge.MemoryItem{{Kind: "finding", Text: "port 22 open"}}}
	got := buildMemoryBlock(context.Background(), fr, "sess-1", "scan the host")
	if !strings.Contains(got, "MEMORY") || !strings.Contains(got, "port 22 open") {
		t.Fatalf("block missing rendered item: %q", got)
	}
	if fr.gotQ.SessionID != "sess-1" || fr.gotQ.Goal != "scan the host" {
		t.Fatalf("Retrieve called with wrong query: %+v", fr.gotQ)
	}
}

func TestBuildMemoryBlock_EmptyOnNoItems(t *testing.T) {
	fr := &fakeRetrieval{items: nil}
	if got := buildMemoryBlock(context.Background(), fr, "sess", "goal"); got != "" {
		t.Fatalf("no items should yield empty block, got %q", got)
	}
}

func TestBuildSkillsBlock_NilRegistrySafe(t *testing.T) {
	if block := buildSkillsBlock(nil, "scan a web target"); block != "" {
		t.Fatalf("nil registry should yield an empty index, got %q", block)
	}
}

// The Eino instruction carries the SKILLS index (names + goal suggestions), not
// playbook bodies. The body reaches the model only through the skill_view tool.
func TestBuildSkillsBlock_IndexNamesMatchingSkillWithoutBody(t *testing.T) {
	reg := skill.New([]skill.Skill{{
		Name: "web-recon", Description: "web recon playbook",
		Triggers: []string{"web"}, Knowledge: "start with whatweb, then gobuster",
	}})
	block := buildSkillsBlock(reg, "assess the web application")
	if !strings.Contains(block, "web-recon") || !strings.Contains(block, "web recon playbook") {
		t.Fatalf("index must name the matched playbook with its description: %q", block)
	}
	if strings.Contains(block, "whatweb") {
		t.Fatalf("index must not carry the playbook body: %q", block)
	}
}

// skill_view returns the body and records the load as an ActSkill activity.
func TestSkillViewTool_ReturnsBodyAndUnknownNameIsRecoverable(t *testing.T) {
	reg := skill.New([]skill.Skill{{
		Name: "web-recon", Description: "web recon playbook",
		Triggers: []string{"web"}, Knowledge: "start with whatweb, then gobuster",
	}})
	tl := newSkillViewTool(skillViewDeps{taskID: "t1", sessionID: "s1", skills: reg})

	out, err := tl.InvokableRun(context.Background(), `{"name":"web-recon"}`)
	if err != nil || !strings.Contains(out, "start with whatweb, then gobuster") {
		t.Fatalf("skill_view must return the full body, got err=%v out=%q", err, out)
	}
	out, err = tl.InvokableRun(context.Background(), `{"name":"nope"}`)
	if err != nil || !strings.Contains(out, "SKILLS index") {
		t.Fatalf("unknown name must be a recoverable message pointing at the index, got err=%v out=%q", err, out)
	}
}

func TestBuildDelegationBlock_EmptyWithNoSubAgents(t *testing.T) {
	if got := buildDelegationBlock(nil, nil); got != "" {
		t.Fatalf("no sub-agents should yield empty block, got %q", got)
	}
}

func TestBuildDelegationBlock_ListsRoles(t *testing.T) {
	subAgents := map[string]agent.SubAgentRunner{"web": &agent.Runner{}, "recon": &agent.Runner{}}
	block := buildDelegationBlock(subAgents, nil)
	if !strings.Contains(block, "web") || !strings.Contains(block, "recon") {
		t.Fatalf("block missing configured roles: %q", block)
	}
}

func TestBuildDelegationBlock_RendersHints(t *testing.T) {
	subAgents := map[string]agent.SubAgentRunner{"web": &agent.Runner{}, "recon": &agent.Runner{}}
	hints := map[string]string{"web": "probe HTTP(S) targets", "recon": ""}
	block := buildDelegationBlock(subAgents, hints)
	if !strings.Contains(block, "web: probe HTTP(S) targets") {
		t.Fatalf("block missing rendered hint for web: %q", block)
	}
	if !strings.Contains(block, "recon\n") {
		t.Fatalf("role with empty hint should render as a bare name: %q", block)
	}
}
