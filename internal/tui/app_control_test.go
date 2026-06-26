package tui

import (
	"strings"
	"testing"
)

func newTestModel() *model {
	return &model{planShown: map[string]bool{}}
}

func countPlanItems(items []TimelineItem) int {
	n := 0
	for _, it := range items {
		if _, ok := it.(PlanItem); ok {
			n++
		}
	}
	return n
}

func TestDecodeFrameExtractsPlan(t *testing.T) {
	frame := `{"type":"task.status","data":{"id":"t1","status":"planned","goal":"recon the host",` +
		`"plan":[{"id":"s1","description":"identify user","status":"pending"},` +
		`{"id":"s2","description":"confirm shell","status":"done"}]}}`
	msg := decodeFrame([]byte(frame))
	tk, ok := msg.(taskMsg)
	if !ok {
		t.Fatalf("expected taskMsg, got %#v", msg)
	}
	if tk.Goal != "recon the host" {
		t.Fatalf("goal not decoded: %q", tk.Goal)
	}
	if len(tk.Plan) != 2 || tk.Plan[0].Description != "identify user" || tk.Plan[1].Status != "done" {
		t.Fatalf("plan not decoded: %+v", tk.Plan)
	}
}

func TestPlanItemRendersStepsAndGlyphs(t *testing.T) {
	out := PlanItem{
		Goal: "enumerate services",
		Steps: []PlanStep{
			{ID: "s1", Description: "scan ports", Status: "done"},
			{ID: "s2", Description: "grab banners", Status: "running"},
			{ID: "s3", Description: "report", Status: "pending"},
		},
	}.Render(80)
	for _, want := range []string{"plan", "enumerate services", "scan ports", "✓", "▶", "○"} {
		if !strings.Contains(out, want) {
			t.Fatalf("plan render missing %q in:\n%s", want, out)
		}
	}
}

func TestOnTaskRendersPlanOnceAndTracksActive(t *testing.T) {
	m := newTestModel()
	planned := taskMsg{ID: "t1", Status: "planned", Goal: "recon",
		Plan: []PlanStep{{ID: "s1", Description: "whoami", Status: "pending"}}}
	m.onTask(planned)
	running := taskMsg{ID: "t1", Status: "running", Goal: "recon",
		Plan: []PlanStep{{ID: "s1", Description: "whoami", Status: "running"}}}
	m.onTask(running)

	if got := countPlanItems(m.items); got != 1 {
		t.Fatalf("plan should be printed exactly once, got %d", got)
	}
	if m.activeTaskID != "t1" {
		t.Fatalf("active task not tracked: %q", m.activeTaskID)
	}
}

func TestOnTaskPausedSetsResumeTarget(t *testing.T) {
	m := newTestModel()
	m.onTask(taskMsg{ID: "t2", Status: "paused", Question: "which subnet?"})
	if m.pausedID != "t2" {
		t.Fatalf("paused task id not set: %q", m.pausedID)
	}
	last := m.items[len(m.items)-1].Render(80)
	if !strings.Contains(last, "which subnet?") || !strings.Contains(last, "/resume") {
		t.Fatalf("paused line should show question + /resume hint: %q", last)
	}
}

func TestOnTaskTerminalClearsState(t *testing.T) {
	m := newTestModel()
	m.activeTaskID, m.pendingID, m.pausedID = "t3", "t3", "t3"
	m.onTask(taskMsg{ID: "t3", Status: "completed", Summary: "done", Findings: []string{"open port 22"}})
	if m.activeTaskID != "" || m.pendingID != "" || m.pausedID != "" {
		t.Fatalf("terminal status must clear task state: active=%q pending=%q paused=%q",
			m.activeTaskID, m.pendingID, m.pausedID)
	}
}

func TestSlashStopWithoutActiveTask(t *testing.T) {
	m := newTestModel()
	cmd := m.handleSlashCommand("/stop")
	if cmd != nil {
		t.Fatal("/stop with no active task should not issue a network command")
	}
	last := m.items[len(m.items)-1].Render(80)
	if !strings.Contains(last, "no active task") {
		t.Fatalf("expected 'no active task' notice, got %q", last)
	}
}

func TestSlashCommandsIncludeControls(t *testing.T) {
	m := newTestModel()
	have := map[string]bool{}
	for _, c := range m.slashCommands() {
		have[c] = true
	}
	for _, want := range []string{"/stop", "/resume", "/auto", "/ask", "/mode"} {
		if !have[want] {
			t.Fatalf("slash command set missing %q: %v", want, m.slashCommands())
		}
	}
}
