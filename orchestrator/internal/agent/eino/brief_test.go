package eino

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/n0connect/yummi/orchestrator/internal/agent"
	"github.com/n0connect/yummi/orchestrator/internal/skill"
)

// The brief leads with the goal, then the parts, memory, skills index, and the
// playbook, in that fixed order. Empty sections are omitted.
func TestComposeTaskBriefOrderAndOmission(t *testing.T) {
	out := composeTaskBrief("assess the web server", []string{"enumerate", "fingerprint"},
		"MEMORY — notes\n  • [web_server] Apache 2.4.49\n", "SKILLS — index\n", "PLAYBOOK for this goal — web\nbody\n")
	goalAt := strings.Index(out, "Goal: assess the web server")
	partsAt := strings.Index(out, "1. enumerate")
	memAt := strings.Index(out, "MEMORY — notes")
	skillAt := strings.Index(out, "SKILLS — index")
	pbAt := strings.Index(out, "PLAYBOOK for this goal")
	if goalAt < 0 || goalAt >= partsAt || partsAt >= memAt || memAt >= skillAt || skillAt >= pbAt {
		t.Fatalf("brief sections out of order:\n%s", out)
	}
	if !strings.Contains(out, "do not call finish until every part") {
		t.Fatalf("a multi-part goal must carry the completion rule:\n%s", out)
	}

	bare := composeTaskBrief("scan one host", nil, "", "", "")
	if strings.Contains(bare, "multiple distinct parts") || strings.Contains(bare, "MEMORY") || strings.Contains(bare, "PLAYBOOK") {
		t.Fatalf("empty sections must be omitted:\n%s", bare)
	}
}

// Every Run's opening message carries the brief, including retries and nudges.
// Each Run starts with no carried history, so a retry without the brief would
// lose the memory the first attempt was given.
func TestPrefixBriefCarriesContextIntoEveryOpeningMessage(t *testing.T) {
	brief := "TASK BRIEF — read it before your first action.\nGoal: x\n"
	if got := prefixBrief(brief, "retry text"); !strings.HasPrefix(got, brief) || !strings.HasSuffix(got, "retry text") {
		t.Fatalf("retry must carry the brief ahead of its own text, got %q", got)
	}
	if got := prefixBrief("", "plain"); got != "plain" {
		t.Fatalf("an empty brief must leave the message untouched, got %q", got)
	}
}

// The single most relevant playbook is pushed once; the rest stay on the index.
func TestBuildPlaybookBlockPushesOnlyMatchingPlaybook(t *testing.T) {
	reg := skill.New([]skill.Skill{
		{Name: "web", Description: "web playbook", Triggers: []string{"web", "http"}, Knowledge: "fingerprint with whatweb"},
		{Name: "smb", Description: "smb playbook", Triggers: []string{"smb"}, Knowledge: "enumerate shares"},
	})
	block, names := buildPlaybookBlock(reg, "assess the web server over http")
	if len(names) != 1 || names[0] != "web" || !strings.Contains(block, "fingerprint with whatweb") {
		t.Fatalf("top match must be pushed with its method, got names=%v block=%q", names, block)
	}
	if strings.Contains(block, "enumerate shares") {
		t.Fatal("only the top playbook may be pushed")
	}
	if b, n := buildPlaybookBlock(reg, "nothing relevant here"); b != "" || len(n) != 0 {
		t.Fatalf("no match must push nothing, got %q / %q", b, n)
	}
	if b, n := buildPlaybookBlock(nil, "web"); b != "" || len(n) != 0 {
		t.Fatal("a nil registry must push nothing")
	}
}

// The system's automatic playbook push must never be recorded as ActSkill
// ("skill_applied") — that kind is reserved for the model's own skill_view
// call (see ActSkill's doc comment). A push the system made on its own is a
// suggestion, not evidence the model is using skills, and conflating the two
// made it impossible to tell from TRACE whether a connected model ever
// actually calls skill_view.
func TestTaskBriefRecordsPushAsSuggestedNotApplied(t *testing.T) {
	store := activityTestStore(t)
	task, err := store.CreateSubtask("sess-brief", "assess the web server", 15, "")
	if err != nil {
		t.Fatalf("CreateSubtask: %v", err)
	}
	reg := skill.New([]skill.Skill{
		{Name: "web", Description: "web playbook", Triggers: []string{"web"}, Knowledge: "fingerprint with whatweb"},
	})
	r := &EinoRunner{store: store, cfg: Config{Skills: reg}}

	r.taskBrief(context.Background(), "sess-brief", task.ID, "assess the web server", nil)

	events := store.ListActivity("sess-brief", 50)
	var suggested, applied int
	for _, e := range events {
		switch e.Kind {
		case agent.ActSkillSuggested:
			suggested++
			if e.Summary != "web" {
				t.Fatalf("expected the pushed playbook's name in Summary, got %q", e.Summary)
			}
		case agent.ActSkill:
			applied++
		}
	}
	if suggested != 1 {
		t.Fatalf("want exactly 1 skill_suggested event for the auto-push, got %d (events=%+v)", suggested, events)
	}
	if applied != 0 {
		t.Fatalf("the auto-push must never emit skill_applied, got %d", applied)
	}
}

// By contrast, the model's own skill_view tool call must emit ActSkill, so the
// two signals stay cleanly distinguishable in the same activity feed.
func TestSkillViewToolRecordsApplied(t *testing.T) {
	store := activityTestStore(t)
	reg := skill.New([]skill.Skill{
		{Name: "web", Description: "d", Triggers: []string{"web"}, Knowledge: "k"},
	})
	tl := newSkillViewTool(skillViewDeps{taskID: "t1", sessionID: "sess-skillview", store: store, skills: reg})

	if _, err := tl.InvokableRun(context.Background(), `{"name":"web"}`); err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}

	events := store.ListActivity("sess-skillview", 50)
	var applied, suggested int
	for _, e := range events {
		switch e.Kind {
		case agent.ActSkill:
			applied++
		case agent.ActSkillSuggested:
			suggested++
		}
	}
	if applied != 1 || suggested != 0 {
		t.Fatalf("want exactly 1 skill_applied and 0 skill_suggested, got applied=%d suggested=%d", applied, suggested)
	}
}

// A very large playbook is cut on a rune boundary, and the cut says where the
// rest lives.
func TestBuildPlaybookBlockTruncatesRuneSafely(t *testing.T) {
	big := strings.Repeat("ğ", playbookCharCap+500)
	reg := skill.New([]skill.Skill{
		{Name: "big", Description: "d", Triggers: []string{"web"}, Knowledge: big},
	})
	block, _ := buildPlaybookBlock(reg, "web target")
	if !strings.Contains(block, "playbook truncated; load the rest with skill_view") {
		t.Fatal("an oversized playbook must say how to load the rest")
	}
	if !strings.Contains(block, strings.Repeat("ğ", 100)) {
		t.Fatal("the cut must keep the playbook's opening text intact")
	}
	if !utf8.ValidString(block) {
		t.Fatal("the cut must not split a multi-byte rune")
	}
}

// Every strong match is pushed, not only the top one: a goal naming two
// services must get both playbooks, so the model never depends on choosing to
// load the second one (live-observed: SNMP work done without its playbook).
func TestBuildPlaybookBlockPushesEveryStrongMatch(t *testing.T) {
	reg := skill.New([]skill.Skill{
		{Name: "smb-enum", Description: "smb", Triggers: []string{"smb"}, Knowledge: "list shares with smbmap"},
		{Name: "snmp-enum", Description: "snmp", Triggers: []string{"snmp"}, Knowledge: "try default community strings"},
		{Name: "web", Description: "web", Triggers: []string{"web"}, Knowledge: "fingerprint with whatweb"},
	})
	block, names := buildPlaybookBlock(reg, "enumerate smb shares and check snmp community strings")
	if len(names) != 2 {
		t.Fatalf("both smb and snmp must be pushed, got %v", names)
	}
	if !strings.Contains(block, "list shares with smbmap") || !strings.Contains(block, "try default community strings") {
		t.Fatalf("both playbooks' methods must be in the block: %q", block)
	}
	if strings.Contains(block, "fingerprint with whatweb") {
		t.Fatal("an unmatched playbook must not be pushed")
	}
}
