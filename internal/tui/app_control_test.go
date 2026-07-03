package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
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

// Regression for the TUI/web parity bug: execMsg previously only carried
// Command/Status/Output/Error, so Mode was always empty and the conductor
// analyze-feedback round-trip (gated on Mode == "assistant_loop") could never
// fire — the TUI silently dropped the post-execution analysis the web UI
// performs. decodeFrame must now surface the sanitized/banner/mode fields too.
func TestDecodeFrameExtractsSanitizedExecFields(t *testing.T) {
	frame := `{"type":"execution.status","data":{"command":"uptime","status":"completed",` +
		`"output":"raw-ansi","sanitized_output":"raw","security_banner":"[BANNER]","mode":"assistant_loop"}}`
	msg := decodeFrame([]byte(frame))
	e, ok := msg.(execMsg)
	if !ok {
		t.Fatalf("expected execMsg, got %#v", msg)
	}
	if e.Mode != "assistant_loop" {
		t.Fatalf("mode not decoded: %q", e.Mode)
	}
	if e.SanitizedOutput != "raw" {
		t.Fatalf("sanitized_output not decoded: %q", e.SanitizedOutput)
	}
	if e.Banner != "[BANNER]" {
		t.Fatalf("security_banner not decoded: %q", e.Banner)
	}
}

func TestAnalyzeExecutionSkipsAgentTaskMode(t *testing.T) {
	m := newTestModel()
	m.client = NewClient("http://unused.invalid")
	// agent_task mode is the agent graph's own loop — the model.Update dispatch
	// (not analyzeExecution itself) is what gates on Mode, so this test instead
	// pins the cap behavior analyzeExecution does own: 3 consecutive calls are
	// allowed, the 4th is refused without making a 4th network call.
	for i := 0; i < 3; i++ {
		if c := m.analyzeExecution(execMsg{Command: "uptime", Status: "completed", Mode: "assistant_loop"}); c == nil {
			t.Fatalf("call %d: expected a chat command, got nil", i+1)
		}
	}
	if c := m.analyzeExecution(execMsg{Command: "uptime", Status: "completed", Mode: "assistant_loop"}); c != nil {
		t.Fatal("expected nil command once the auto-feedback cap is exceeded")
	}
	found := false
	for _, it := range m.items {
		if s, ok := it.(SystemItem); ok && strings.Contains(s.Text, "Otomatik deneme limiti") {
			found = true
		}
	}
	if !found {
		t.Fatal("expected a system item announcing the auto-feedback cap")
	}
}

// Regression: bubbles/viewport's DefaultKeyMap binds plain "j"/"k"/"h"/"l"/
// "u"/"d"/"f"/"b"/space as pager scroll keys. Update() used to forward every
// tea.Msg — including ordinary typed characters — to the viewport, so typing
// any of those letters into a message scrolled the timeline out from under
// the operator mid-sentence. Only mouse events should reach the viewport.
func TestTypingDoesNotScrollViewport(t *testing.T) {
	m := newTestModel()
	m.client = NewClient("http://unused.invalid")
	m.input = textinput.New()
	m.input.Focus()
	m.ready = true
	m.width, m.height = 80, 24

	m.viewport = viewport.New(40, 5)
	m.viewport.SetContent(strings.Repeat("line\n", 50))
	m.viewport.YOffset = 0

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	got := updated.(model)

	if got.viewport.YOffset != 0 {
		t.Fatalf("typing 'j' scrolled the viewport: YOffset=%d", got.viewport.YOffset)
	}
	if got.input.Value() != "j" {
		t.Fatalf("'j' should still reach the input box, got %q", got.input.Value())
	}
}

// Regression: textinput.Width defaults to 0, which bubbles/textinput treats
// as "never clip" — long input bled past the box's right border instead of
// scrolling in place. WindowSizeMsg must bound input.Width to the box.
func TestWindowResizeBoundsInputWidth(t *testing.T) {
	m := newTestModel()
	m.client = NewClient("http://unused.invalid")
	m.input = textinput.New()

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	got := updated.(model)

	if got.input.Width <= 0 {
		t.Fatalf("expected a bounded input width after resize, got %d", got.input.Width)
	}
	if got.input.Width >= 60 {
		t.Fatalf("input width %d should be smaller than the terminal width (borders+prompt)", got.input.Width)
	}
}

// Regression: pasting a large block used to dump it verbatim into the
// single-line input, overflowing the box border. A bracketed paste above the
// threshold must collapse to a "[Pasted #N]" placeholder, and sendChat must
// expand it back to the real content before it reaches the conductor.
func TestLargePasteCollapsesToPlaceholderAndExpandsOnSend(t *testing.T) {
	var gotMessage string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]string
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotMessage = req["message"]
		_, _ = w.Write([]byte(`{"reply":"ok","session_id":"s1"}`))
	}))
	defer srv.Close()

	m := newTestModel()
	m.client = NewClient(srv.URL)
	m.client.SessionID = "s1"
	m.input = textinput.New()
	m.input.Focus()

	pasted := strings.Repeat("x", pasteCollapseThreshold+1)
	_, handled := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(pasted), Paste: true})
	if !handled {
		t.Fatal("expected a large paste to be handled (collapsed), not passed through")
	}
	if m.input.Value() != "[Pasted #1]" {
		t.Fatalf("expected the placeholder in the input, got %q", m.input.Value())
	}
	if len(m.pastedBlocks) != 1 || m.pastedBlocks[0] != pasted {
		t.Fatalf("pasted content not stored for later expansion: %+v", m.pastedBlocks)
	}

	cmd := m.sendChat()
	if cmd == nil {
		t.Fatal("sendChat returned a nil command")
	}
	cmd() // synchronously runs the chat round-trip against the test server

	if gotMessage != pasted {
		t.Fatalf("placeholder was not expanded back to the pasted content before sending; got len=%d", len(gotMessage))
	}
	if len(m.pastedBlocks) != 0 {
		t.Fatal("pastedBlocks should be cleared after the message is sent")
	}
}

// Regression: a sequential loop of strings.ReplaceAll (one per stored block,
// lowest index first) re-scans the *whole growing string* on every pass —
// including text just spliced in by an earlier substitution. If block #1's
// own pasted content happened to literally contain the text "[Pasted #2]",
// the later pass for block #2 would match that occurrence too, splicing
// block #2's content into the middle of block #1's text instead of leaving
// it untouched. expandPastedBlocks must substitute in a single pass over the
// original text so a placeholder produced by expansion is never re-matched.
func TestExpandPastedBlocksDoesNotSpliceAcrossBlocks(t *testing.T) {
	m := newTestModel()
	m.pastedBlocks = []string{
		"block one contains the literal text [Pasted #2] inside it",
		"block two's real content",
	}

	got := m.expandPastedBlocks("intro [Pasted #1] middle [Pasted #2] end")
	want := "intro block one contains the literal text [Pasted #2] inside it middle block two's real content end"

	if got != want {
		t.Fatalf("cross-block splicing occurred:\n got:  %q\nwant: %q", got, want)
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

// Regression: the gateway's Ask egress mode holds a command in
// awaiting_egress_approval until the operator approves/denies its external
// target. The web UI (AssistantPanel.tsx's egressHolds) already renders this
// as an approve/reject card; the TUI had no equivalent at all — an operator
// working from the terminal had no way to ever see or resolve one of these
// holds. decodeFrame must surface the execution id and egress_target fields
// an egress hold needs (previously undecoded — wireExec had neither).
func TestDecodeFrameExtractsEgressHoldFields(t *testing.T) {
	frame := `{"type":"execution.status","data":{"id":"exec1","command":"curl evil.example",` +
		`"status":"awaiting_egress_approval","egress_target":"evil.example"}}`
	msg := decodeFrame([]byte(frame))
	e, ok := msg.(execMsg)
	if !ok {
		t.Fatalf("expected execMsg, got %#v", msg)
	}
	if e.ID != "exec1" {
		t.Fatalf("id not decoded: %q", e.ID)
	}
	if e.EgressTarget != "evil.example" {
		t.Fatalf("egress_target not decoded: %q", e.EgressTarget)
	}
}

// An awaiting_egress_approval execMsg must be tracked as a pending hold, not
// dropped into the timeline like a normal exec result (the operator hasn't
// seen an outcome yet — the command hasn't run).
func TestExecMsgAwaitingEgressApprovalAddsHold(t *testing.T) {
	m := newTestModel()
	updated, _ := m.Update(execMsg{ID: "exec1", Command: "curl evil.example", Status: "awaiting_egress_approval", EgressTarget: "evil.example"})
	got := updated.(model)

	if len(got.egressHolds) != 1 || got.egressHolds[0].ID != "exec1" {
		t.Fatalf("expected 1 egress hold for exec1, got %+v", got.egressHolds)
	}
	for _, it := range got.items {
		if _, ok := it.(ExecItem); ok {
			t.Fatalf("awaiting_egress_approval must not be logged as a normal exec result: %+v", it)
		}
	}
}

// Once the held execution reaches a terminal status, it must leave the hold
// list (mirrors AssistantPanel.tsx's onExecution filtering egressHolds on
// completed/failed/canceled).
func TestExecMsgTerminalClearsEgressHold(t *testing.T) {
	m := newTestModel()
	m.egressHolds = []execMsg{{ID: "exec1", Command: "curl evil.example", EgressTarget: "evil.example"}}
	updated, _ := m.Update(execMsg{ID: "exec1", Command: "curl evil.example", Status: "completed", Output: "ok"})
	got := updated.(model)

	if len(got.egressHolds) != 0 {
		t.Fatalf("expected the resolved hold to be removed, got %+v", got.egressHolds)
	}
}

// 'y'/'n' must resolve the oldest egress hold when one is pending and there's
// no active proposal/task-approval ahead of it in the priority chain (mirrors
// the existing proposal/pendingID y-n routing in handleKey).
func TestYNKeyResolvesEgressHold(t *testing.T) {
	m := newTestModel()
	m.client = NewClient("http://unused.invalid")
	m.input = textinput.New()
	m.egressHolds = []execMsg{{ID: "exec1", Command: "curl evil.example", EgressTarget: "evil.example"}}

	cmd, handled := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if !handled {
		t.Fatal("expected 'y' to be consumed as an egress-hold approval")
	}
	if cmd == nil {
		t.Fatal("expected a network command to resolve the egress hold")
	}
	if len(m.egressHolds) != 0 {
		t.Fatalf("expected the hold to be optimistically cleared, got %+v", m.egressHolds)
	}
}

// Regression: ChatResult.TaskGoal was decoded off the chat response but never
// surfaced anywhere — an operator starting an agent task from chat saw only
// "task started — watching…" with no indication of what goal was actually
// kicked off, until the first taskMsg with a plan arrived over WS (which can
// lag). The "task started" notice must include the goal when the server sent
// one.
func TestChatResultTaskStartedIncludesGoal(t *testing.T) {
	m := newTestModel()
	updated, _ := m.Update(chatResultMsg{res: ChatResult{TaskID: "t1", TaskGoal: "recon the host"}})
	got := updated.(model)

	last := got.items[len(got.items)-1].Render(80)
	if !strings.Contains(last, "recon the host") {
		t.Fatalf("expected task-started notice to include the goal, got %q", last)
	}
}
