package skill

import (
	"fmt"
	"strings"
	"testing"
)

func sk(name, desc string, triggers ...string) Skill {
	return Skill{Name: name, Description: desc, Triggers: triggers, Knowledge: "method for " + name}
}

func TestIndexListsSuggestedFirstWithDescriptionAndOthersByName(t *testing.T) {
	reg := New([]Skill{
		sk("zeta", "Zeta playbook.", "zeta"),
		sk("alpha", "Alpha playbook for SMB shares.", "smb"),
		sk("mid", "Unrelated playbook."),
	})
	idx := reg.Index("enumerate the smb shares", nil, 0)

	if !strings.Contains(idx, "Suggested for this goal:\n  - alpha: Alpha playbook for SMB shares.") {
		t.Fatalf("goal-matched skill must be listed first with its description:\n%s", idx)
	}
	if strings.Contains(idx, "- zeta:") || strings.Contains(idx, "- mid:") {
		t.Fatalf("non-matched skills must not get a description line:\n%s", idx)
	}
	if !strings.Contains(idx, "Other playbooks: mid, zeta") {
		t.Fatalf("other skills must be listed by name, sorted:\n%s", idx)
	}
	if !strings.Contains(idx, "skill_view(name)") {
		t.Fatalf("index must tell the model how to load a playbook:\n%s", idx)
	}
}

func TestIndexNoSuggestionStillListsEverySkillByName(t *testing.T) {
	reg := New([]Skill{sk("one", "d", "x"), sk("two", "d", "y")})
	idx := reg.Index("unrelated goal", nil, 0)
	if strings.Contains(idx, "Suggested for this goal") {
		t.Fatalf("no trigger matched, so no suggestion section expected:\n%s", idx)
	}
	if !strings.Contains(idx, "Other playbooks: one, two") {
		t.Fatalf("every skill must still be discoverable by name:\n%s", idx)
	}
}

func TestIndexBudgetBoundsBlockAndReportsOverflow(t *testing.T) {
	var many []Skill
	for i := range 200 {
		many = append(many, sk(fmt.Sprintf("skill-%03d", i), "d"))
	}
	reg := New(many)
	const budget = 400
	idx := reg.Index("nothing matches", nil, budget)
	if len(idx) > budget+40 {
		t.Fatalf("index %d bytes exceeds budget %d (+ one overflow note)", len(idx), budget)
	}
	if !strings.Contains(idx, "more)") {
		t.Fatalf("truncated index must report how many names were omitted:\n%s", idx)
	}
}

func TestIndexEmptyRegistryAndNil(t *testing.T) {
	if got := New(nil).Index("goal", nil, 0); got != "" {
		t.Fatalf("empty registry must render nothing, got %q", got)
	}
	var nilReg *Registry
	if got := nilReg.Index("goal", nil, 0); got != "" {
		t.Fatalf("nil registry must render nothing, got %q", got)
	}
}

func TestLookupFindsExactNameOnly(t *testing.T) {
	reg := New([]Skill{sk("recon", "d", "scan")})
	if s, ok := reg.Lookup(" recon "); !ok || s.Name != "recon" {
		t.Fatalf("Lookup should trim and find recon, got %+v ok=%v", s, ok)
	}
	if _, ok := reg.Lookup("rec"); ok {
		t.Fatal("Lookup must match the exact name, not a prefix")
	}
	var nilReg *Registry
	if _, ok := nilReg.Lookup("recon"); ok {
		t.Fatal("nil registry must not panic and must find nothing")
	}
}

func TestRenderBodyCarriesKnowledgeAndPlanConstraint(t *testing.T) {
	body := RenderBody(Skill{
		Name: "recon", Description: "Host discovery.", Knowledge: "  step one\nstep two  ", PlanStepLimit: 2,
	})
	for _, want := range []string{"SKILL recon — Host discovery.", "step one\nstep two", "at most 2 step(s)"} {
		if !strings.Contains(body, want) {
			t.Fatalf("RenderBody missing %q:\n%s", want, body)
		}
	}
}

func TestOneLineIsRuneSafe(t *testing.T) {
	got := oneLine("ğüşıöç ÇĞÜŞİÖ çok uzun bir açıklama metni", 10)
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("long text must end with an ellipsis, got %q", got)
	}
	if n := len([]rune(got)); n != 10 {
		t.Fatalf("want exactly 10 runes, got %d (%q)", n, got)
	}
}

// Integration against the real shipped catalogue, not a fixture: the index for
// a real goal must stay within the budget even with ~100 playbooks loaded.
func TestIndexOverShippedCatalogueStaysWithinBudget(t *testing.T) {
	reg, err := LoadDir("../../../persona/v0.1/skills")
	if err != nil {
		t.Fatalf("load shipped skills: %v", err)
	}
	if len(reg.All()) < 50 {
		t.Fatalf("expected the shipped catalogue, got %d skills", len(reg.All()))
	}
	idx := reg.Index("scan 198.51.100.10 for open ports and web services", nil, 0)
	if len(idx) > DefaultIndexBudget+40 {
		t.Fatalf("shipped index %d bytes exceeds budget %d", len(idx), DefaultIndexBudget)
	}
	if !strings.Contains(idx, "recon") {
		t.Fatalf("a port-scan goal should suggest the recon playbook:\n%s", idx)
	}
}
