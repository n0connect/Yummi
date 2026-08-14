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
	block, names := buildSkillsBlock(nil, "scan a web target")
	if block != "" || len(names) != 0 {
		t.Fatalf("nil registry should yield empty block/names, got block=%q names=%v", block, names)
	}
}

func TestBuildSkillsBlock_SelectsAndRendersMatchingSkill(t *testing.T) {
	reg := skill.New([]skill.Skill{{
		Name: "web-recon", Description: "web recon playbook",
		Triggers: []string{"web"}, Knowledge: "start with whatweb, then gobuster",
	}})
	block, names := buildSkillsBlock(reg, "assess the web application")
	if len(names) != 1 || names[0] != "web-recon" {
		t.Fatalf("expected web-recon selected, got names=%v", names)
	}
	if !strings.Contains(block, "web-recon") || !strings.Contains(block, "whatweb") {
		t.Fatalf("rendered block missing skill content: %q", block)
	}
}

func TestBuildDelegationBlock_EmptyWithNoSubAgents(t *testing.T) {
	if got := buildDelegationBlock(nil); got != "" {
		t.Fatalf("no sub-agents should yield empty block, got %q", got)
	}
}

func TestBuildDelegationBlock_ListsRoles(t *testing.T) {
	subAgents := map[string]*agent.Runner{"web": {}, "recon": {}}
	block := buildDelegationBlock(subAgents)
	if !strings.Contains(block, "web") || !strings.Contains(block, "recon") {
		t.Fatalf("block missing configured roles: %q", block)
	}
}
