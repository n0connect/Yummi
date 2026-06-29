package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── Command result messages ──────────────────────────────────────────────────

type sessionCreatedMsg struct{ err error }
type chatResultMsg struct {
	res ChatResult
	err error
}
type approveDoneMsg struct{ err error }
type healthMsg struct{ mods []Module }
type healthTickMsg struct{}

// cmdResultMsg is the async result of an operator control command (/stop,
// /resume, /auto, /ask, /mode). note (if set) is appended to the timeline;
// mode (if set) updates the header approval-mode indicator.
type cmdResultMsg struct {
	note string
	mode string
}

// model is the TUI operator console state.
type model struct {
	client *Client
	base   string
	stream chan tea.Msg

	viewport viewport.Model
	input    textinput.Model

	items        []TimelineItem
	history      []string
	historyIndex int
	slashMenuIdx int

	proposals []Proposal // active single-command proposals (first is acted on)
	pendingID string     // agent task awaiting approval
	pendingPr string     // its proposal id
	pendingCm string     // its command (banner)

	activeTaskID string          // non-terminal task currently being tracked
	pausedID     string          // task paused awaiting /resume
	mode         string          // agent approval mode: auto | ask_permission
	planShown    map[string]bool // task ids whose plan has been printed (once)

	// autoFeedbackCount counts consecutive auto feed-backs (analyzeExecution)
	// sent since the last user message, capped to break infinite retry loops —
	// mirrors AssistantPanel.tsx's autoFeedbackCountRef in the web UI.
	autoFeedbackCount int

	// pastedBlocks holds the full content of large/multi-line pastes collapsed
	// to a "[Pasted #N]" placeholder in the input — textinput is single-line, so
	// a raw paste either overflows past the box border or corrupts on newlines.
	// Cleared once the message is sent; substituted back in at send time.
	pastedBlocks []string

	modules []Module
	loading bool
	ready   bool
	width   int
	height  int
}

// Run starts the Bubble Tea operator console against the orchestrator at apiURL.
func Run(apiURL string) error {
	ti := textinput.New()
	ti.Placeholder = "Tell Yummi what to do…"
	ti.PlaceholderStyle = lipgloss.NewStyle().Foreground(colMuted)
	ti.Focus()
	ti.CharLimit = 500
	ti.Prompt = "❯ "
	ti.PromptStyle = inputPrompt
	ti.TextStyle = inputTextStyle

	m := model{
		client:       NewClient(apiURL),
		base:         apiURL,
		input:        ti,
		history:      []string{},
		historyIndex: 0,
		planShown:    map[string]bool{},
		items: []TimelineItem{
			RawItem{Text: "  " + welcomeLogoStyle.Render("Yummi TUI") + " " + welcomeSubStyle.Render("v1.0")},
			RawItem{Text: "  " + statusStyle.Render("Agentic orchestration console")},
			RawItem{Text: ""},
			SystemItem{Text: "connecting…"},
		},
	}
	_, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	return err
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		textinput.Blink,
		func() tea.Msg { return sessionCreatedMsg{err: m.client.EnsureSession()} },
		func() tea.Msg { return healthMsg{mods: m.client.Health()} },
		func() tea.Msg {
			// Surface the agent's approval mode in the header from the start.
			mode, err := m.client.ApprovalMode()
			if err != nil {
				return cmdResultMsg{}
			}
			return cmdResultMsg{mode: mode}
		},
	)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if c, handled := m.handleKey(msg); handled {
			return m, c
		}

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		vpWidth := msg.Width - 2
		if vpWidth < 10 {
			vpWidth = 10
		}
		if !m.ready {
			m.viewport = viewport.New(vpWidth, 1)
			m.ready = true
		} else {
			m.viewport.Width = vpWidth
		}
		// Bound the input to the box's actual content width (border + prompt)
		// so typing past it scrolls in place like a normal shell prompt instead
		// of bleeding text past the right border — textinput.Width defaults to
		// 0, which bubbles/textinput treats as "unbounded", never clipping.
		inputWidth := m.width - 4 - lipgloss.Width(m.input.Prompt)
		if inputWidth < 1 {
			inputWidth = 1
		}
		m.input.Width = inputWidth
		m.refresh()

	case sessionCreatedMsg:
		if msg.err != nil {
			m.append(SystemItem{Text: "session init failed: " + msg.err.Error()})
		} else {
			m.append(SystemItem{Text: fmt.Sprintf("session active [%s] — ready", short(m.client.SessionID))})
			m.stream = newStream(m.base, m.client.SessionID)
			cmds = append(cmds, waitForEvent(m.stream), tickHealth())
		}

	case chatResultMsg:
		m.loading = false
		if msg.err != nil {
			m.append(SystemItem{Text: "error: " + msg.err.Error()})
			break
		}
		if r := strings.TrimSpace(msg.res.Reply); r != "" {
			m.append(AgentItem{Text: r})
		}
		if len(msg.res.Proposals) > 0 {
			m.proposals = msg.res.Proposals
		} else if msg.res.TaskID != "" {
			m.append(SystemItem{Text: "task started — watching…"})
		}

	case execMsg:
		// Only log terminal states to the timeline to avoid spamming ✗ for "started" or "running".
		if msg.Status == "completed" || msg.Status == "failed" || msg.Status == "canceled" {
			m.append(ExecItem{Command: msg.Command, Status: msg.Status, Output: firstNonEmpty(msg.Output, msg.Error)})
		}
		// Feed completed/failed results from the single-command chat loop back to
		// the conductor so it can analyze the output, same as AssistantPanel.tsx's
		// analyzeOutput — skip 'canceled' (user-initiated) and agent_task mode (the
		// agent graph handles its own observation loop internally).
		if (msg.Status == "completed" || msg.Status == "failed") && msg.Mode == "assistant_loop" {
			if c := m.analyzeExecution(msg); c != nil {
				cmds = append(cmds, c)
			}
		}
		cmds = append(cmds, waitForEvent(m.stream))

	case taskMsg:
		m.onTask(msg)
		cmds = append(cmds, waitForEvent(m.stream))

	case activityMsg:
		a := ActivityItem{Kind: msg.Kind, Summary: msg.Summary, Nested: msg.Parent != ""}
		if a.Render(m.width) != "" {
			m.append(a)
		}
		cmds = append(cmds, waitForEvent(m.stream))

	case wsErrMsg:
		m.append(SystemItem{Text: msg.note})
		cmds = append(cmds, waitForEvent(m.stream))

	case approveDoneMsg:
		if msg.err != nil {
			m.append(SystemItem{Text: "approve failed: " + msg.err.Error()})
		}

	case cmdResultMsg:
		if msg.mode != "" {
			m.mode = msg.mode
		}
		if msg.note != "" {
			m.append(SystemItem{Text: msg.note})
		}

	case healthMsg:
		m.modules = msg.mods

	case healthTickMsg:
		cmds = append(cmds, func() tea.Msg { return healthMsg{mods: m.client.Health()} }, tickHealth())
	}

	m.updateViewportHeight()

	var c tea.Cmd
	m.input, c = m.input.Update(msg)
	cmds = append(cmds, c)
	// Only forward mouse events (wheel scroll) to the viewport. Forwarding every
	// tea.Msg used to mean any letter the user typed that happens to collide with
	// viewport's pager keymap (j/k/h/l/u/d/f/b, space, pgup/pgdown — vim-style
	// bindings baked into bubbles/viewport's DefaultKeyMap) scrolled the timeline
	// out from under them mid-sentence. Arrow-key history navigation is handled
	// entirely in handleKey above and never reaches here.
	if _, ok := msg.(tea.MouseMsg); ok {
		m.viewport, c = m.viewport.Update(msg)
		cmds = append(cmds, c)
	}
	return m, tea.Batch(cmds...)
}

// pasteCollapseThreshold is the rune count above which a bracketed paste gets
// collapsed to a "[Pasted #N]" placeholder rather than inserted verbatim.
const pasteCollapseThreshold = 200

// insertPastePlaceholder stores runes as a new pasted block and splices its
// "[Pasted #N]" placeholder into the input at the current cursor position.
func (m *model) insertPastePlaceholder(runes []rune) tea.Cmd {
	m.pastedBlocks = append(m.pastedBlocks, string(runes))
	placeholder := fmt.Sprintf("[Pasted #%d]", len(m.pastedBlocks))

	val := []rune(m.input.Value())
	pos := m.input.Position()
	if pos > len(val) {
		pos = len(val)
	}
	newVal := string(val[:pos]) + placeholder + string(val[pos:])
	m.input.SetValue(newVal)
	m.input.SetCursor(pos + len(placeholder))
	return nil
}

// expandPastedBlocks substitutes every "[Pasted #N]" placeholder in text with
// the full content it stands in for, so the conductor sees the real pasted
// content even though the operator only ever saw a compact placeholder.
func (m *model) expandPastedBlocks(text string) string {
	for i, block := range m.pastedBlocks {
		text = strings.ReplaceAll(text, fmt.Sprintf("[Pasted #%d]", i+1), block)
	}
	return text
}

func (m *model) slashCommands() []string {
	return []string{"/stop", "/resume", "/auto", "/ask", "/mode", "/help", "/clear", "/quit"}
}

// handleKey processes keys that the console consumes; returns handled=true to
// short-circuit the normal input/viewport update.
func (m *model) handleKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	inSlashMenu := strings.HasPrefix(m.input.Value(), "/")

	switch msg.Type {
	case tea.KeyCtrlC, tea.KeyEsc:
		return tea.Quit, true

	case tea.KeyUp:
		if inSlashMenu {
			m.slashMenuIdx--
			if m.slashMenuIdx < 0 {
				m.slashMenuIdx = len(m.slashCommands()) - 1
			}
			return nil, true
		}
		if len(m.history) > 0 {
			if m.historyIndex > 0 {
				m.historyIndex--
			}
			m.input.SetValue(m.history[m.historyIndex])
			m.input.SetCursor(len(m.input.Value()))
		}
		return nil, true

	case tea.KeyDown:
		if inSlashMenu {
			m.slashMenuIdx = (m.slashMenuIdx + 1) % len(m.slashCommands())
			return nil, true
		}
		if m.historyIndex < len(m.history) {
			m.historyIndex++
			if m.historyIndex == len(m.history) {
				m.input.SetValue("")
			} else {
				m.input.SetValue(m.history[m.historyIndex])
				m.input.SetCursor(len(m.input.Value()))
			}
		}
		return nil, true

	case tea.KeyEnter:
		if m.loading {
			return nil, true
		}
		// Empty input + active proposal/pending → Enter approves.
		if strings.TrimSpace(m.input.Value()) == "" {
			if len(m.proposals) > 0 {
				return m.approveProposal(), true
			}
			if m.pendingID != "" {
				return m.approvePending(true), true
			}
			return nil, true
		}
		if inSlashMenu {
			return m.handleSlashCommand(m.slashCommands()[m.slashMenuIdx]), true
		}
		return m.sendChat(), true
	case tea.KeyRunes:
		// Large or multi-line pastes arrive as one bracketed-paste KeyMsg (Paste
		// is only ever true for an actual terminal paste, never normal typing).
		// Collapse them to a placeholder instead of dumping raw text into the
		// single-line input, where they'd either overflow the box or, for
		// multi-line content, render as a corrupted single line.
		if msg.Paste && (len(msg.Runes) > pasteCollapseThreshold || strings.ContainsRune(string(msg.Runes), '\n')) {
			return m.insertPastePlaceholder(msg.Runes), true
		}
		// 'y'/'n' act as approve/reject ONLY when the input is empty and an
		// approval is pending — otherwise they are normal characters.
		if strings.TrimSpace(m.input.Value()) == "" && len(msg.Runes) == 1 {
			switch msg.Runes[0] {
			case 'y', 'Y':
				if len(m.proposals) > 0 {
					return m.approveProposal(), true
				}
				if m.pendingID != "" {
					return m.approvePending(true), true
				}
			case 'n', 'N':
				if len(m.proposals) > 0 {
					m.proposals = nil
					m.append(SystemItem{Text: "proposal dismissed"})
					return nil, true
				}
				if m.pendingID != "" {
					return m.approvePending(false), true
				}
			}
		}
	}
	return nil, false
}

// analyzeExecution feeds a completed/failed single-command result back to the
// conductor so it can analyze the output, retry on error, or try a different
// approach — the terminal-console mirror of AssistantPanel.tsx's analyzeOutput.
// Capped at 3 consecutive auto feed-backs per user turn to prevent infinite loops.
func (m *model) analyzeExecution(msg execMsg) tea.Cmd {
	m.autoFeedbackCount++
	if m.autoFeedbackCount > 3 {
		m.append(SystemItem{Text: "Otomatik deneme limiti aşıldı. Farklı bir komut denemek için yazın."})
		return nil
	}

	// Use the ANSI-stripped copies for anything fed back into the LLM — the raw
	// output may contain escape sequences a hostile target could use to smuggle
	// a payload past the banner scan.
	output := strings.TrimSpace(firstNonEmpty(msg.SanitizedOutput, msg.Output))
	errText := strings.TrimSpace(firstNonEmpty(msg.SanitizedError, msg.Error))

	guard := ""
	if banner := strings.TrimSpace(msg.Banner); banner != "" {
		guard = banner + "\n\n"
	}

	var feedback string
	if msg.Status == "failed" {
		detail := firstNonEmpty(firstNonEmpty(errText, output), "exit code 1")
		feedback = guard +
			fmt.Sprintf("[KOMUT BAŞARISIZ] `%s`\n\nHata çıktısı:\n```\n%s\n```\n\n", msg.Command, detail) +
			"Komut başarısız oldu. FARKLI bir komut seç — aynı komutu tekrar deneme."
	} else {
		feedback = guard +
			fmt.Sprintf("[KOMUT TAMAMLANDI] `%s`\n\nÇıktı:\n```\n%s\n```\n\n", msg.Command, firstNonEmpty(output, "(çıktı yok)")) +
			"Bu sonucu analiz et ve kullanıcının sorusunu yanıtla."
	}

	m.loading = true
	return func() tea.Msg {
		res, err := m.client.Chat(feedback)
		return chatResultMsg{res: res, err: err}
	}
}

func (m *model) sendChat() tea.Cmd {
	text := strings.TrimSpace(m.input.Value())

	// Add to history
	if text != "" {
		m.history = append(m.history, text)
		m.historyIndex = len(m.history)
	}

	m.input.Reset()
	m.proposals = nil
	m.slashMenuIdx = 0
	m.autoFeedbackCount = 0

	if strings.HasPrefix(text, "/") {
		return m.handleSlashCommand(text)
	}

	// Expand any "[Pasted #N]" placeholders back to their full content for the
	// conductor — the operator only ever sees the compact placeholder in the
	// timeline, matching what was typed/sent.
	sendText := m.expandPastedBlocks(text)
	m.pastedBlocks = nil

	m.append(UserItem{Text: text})
	m.loading = true
	return func() tea.Msg {
		res, err := m.client.Chat(sendText)
		return chatResultMsg{res: res, err: err}
	}
}

func (m *model) handleSlashCommand(cmd string) tea.Cmd {
	m.input.Reset()
	m.slashMenuIdx = 0

	switch cmd {
	case "/stop":
		if m.activeTaskID == "" {
			m.append(SystemItem{Text: "no active task to stop"})
			return nil
		}
		id := m.activeTaskID
		return func() tea.Msg {
			if err := m.client.StopTask(id); err != nil {
				return cmdResultMsg{note: "stop failed: " + err.Error()}
			}
			return cmdResultMsg{note: "task stopped"}
		}
	case "/resume":
		if m.pausedID == "" {
			m.append(SystemItem{Text: "no paused task to resume"})
			return nil
		}
		id := m.pausedID
		m.pausedID = ""
		return func() tea.Msg {
			if err := m.client.ResumeTask(id); err != nil {
				return cmdResultMsg{note: "resume failed: " + err.Error()}
			}
			return cmdResultMsg{note: "task resumed — watching…"}
		}
	case "/auto":
		return func() tea.Msg {
			if err := m.client.SetApprovalMode("auto"); err != nil {
				return cmdResultMsg{note: "mode change failed: " + err.Error()}
			}
			return cmdResultMsg{note: "approval mode → auto (agent runs commands without asking)", mode: "auto"}
		}
	case "/ask":
		return func() tea.Msg {
			if err := m.client.SetApprovalMode("ask_permission"); err != nil {
				return cmdResultMsg{note: "mode change failed: " + err.Error()}
			}
			return cmdResultMsg{note: "approval mode → ask_permission (every command needs your approval)", mode: "ask_permission"}
		}
	case "/mode":
		return func() tea.Msg {
			mode, err := m.client.ApprovalMode()
			if err != nil {
				return cmdResultMsg{note: "mode query failed: " + err.Error()}
			}
			return cmdResultMsg{note: "approval mode: " + mode, mode: mode}
		}
	case "/clear":
		m.items = []TimelineItem{SystemItem{Text: "timeline cleared."}}
		m.refresh()
		return nil
	case "/quit", "/exit":
		return tea.Quit
	case "/help":
		m.append(SystemItem{Text: "Available commands:\n" +
			"  /stop   - Stop the active agent task\n" +
			"  /resume - Resume a paused task\n" +
			"  /auto   - Agent runs commands without asking\n" +
			"  /ask    - Agent asks approval for every command\n" +
			"  /mode   - Show current approval mode\n" +
			"  /clear  - Clear timeline\n" +
			"  /quit   - Exit application\n" +
			"  /help   - Show this help"})
		return nil
	default:
		m.append(SystemItem{Text: "Unknown command: " + cmd})
		return nil
	}
}

func (m *model) approveProposal() tea.Cmd {
	p := m.proposals[0]
	m.proposals = nil
	m.append(SystemItem{Text: "running: " + p.Command})
	id := p.ID
	return func() tea.Msg { return approveDoneMsg{err: m.client.Approve(id)} }
}

func (m *model) approvePending(approved bool) tea.Cmd {
	taskID, propID := m.pendingID, m.pendingPr
	m.pendingID, m.pendingPr, m.pendingCm = "", "", ""
	verb := "approved"
	if !approved {
		verb = "denied"
	}
	m.append(SystemItem{Text: "command " + verb})
	return func() tea.Msg { return approveDoneMsg{err: m.client.ApproveTask(taskID, propID, approved)} }
}

func (m *model) onTask(t taskMsg) {
	// Print the plan once, as soon as it exists — this is the LLM's primary
	// planning artifact and the operator's window into the agent's intent.
	if len(t.Plan) > 0 && !m.planShown[t.ID] {
		m.planShown[t.ID] = true
		m.append(PlanItem{Goal: t.Goal, Steps: t.Plan})
	}

	switch t.Status {
	case "planning", "planned", "running", "retrying_llm", "summarizing":
		m.activeTaskID = t.ID

	case "awaiting_approval":
		m.activeTaskID = t.ID
		if t.PendingCommand != "" {
			m.pendingID, m.pendingPr, m.pendingCm = t.ID, t.PendingPropID, t.PendingCommand
		}

	case "paused":
		m.activeTaskID = t.ID
		m.pausedID = t.ID
		q := t.Question
		if q == "" {
			q = "agent needs operator input"
		}
		m.append(SystemItem{Text: "agent paused — " + q + "   (type /resume to continue)"})

	case "completed", "failed", "stopped", "interrupted":
		if t.Summary != "" {
			m.append(AgentItem{Text: t.Summary})
		} else if t.LastError != "" {
			m.append(SystemItem{Text: "task " + t.Status + ": " + t.LastError})
		} else {
			m.append(SystemItem{Text: "task " + t.Status})
		}
		for _, f := range t.Findings {
			m.append(ActivityItem{Kind: "finding", Summary: f, Nested: false})
		}
		m.activeTaskID, m.pausedID = "", ""
		m.pendingID, m.pendingPr, m.pendingCm = "", "", ""
	}
}

func (m *model) append(item TimelineItem) {
	m.items = append(m.items, item)
	m.refresh()
}

func (m *model) updateViewportHeight() {
	if !m.ready {
		return
	}
	occupied := 6
	if len(m.proposals) > 0 || m.pendingID != "" {
		occupied = 8
	}
	if strings.HasPrefix(m.input.Value(), "/") {
		occupied += len(m.slashCommands()) // Menu lines
	}
	vpHeight := m.height - occupied
	if vpHeight < 1 {
		vpHeight = 1
	}
	m.viewport.Height = vpHeight
}

func (m *model) refresh() {
	if !m.ready {
		return
	}
	m.updateViewportHeight()
	var rendered []string
	vpWidth := m.width - 2
	if vpWidth < 10 {
		vpWidth = 10
	}
	for _, item := range m.items {
		if r := item.Render(vpWidth); r != "" {
			rendered = append(rendered, r)
		}
	}
	m.viewport.SetContent(strings.Join(rendered, "\n"))
	m.viewport.GotoBottom()
}

func (m model) View() string {
	if !m.ready {
		return "\n  Initializing…"
	}

	// 1. Framed Timeline
	timelineStr := m.renderFramedTimeline(m.viewport.View())

	// 2. Proposal Banner / Menu
	var banner string
	switch {
	case len(m.proposals) > 0:
		p := m.proposals[0]
		left := proposalLabelStyle.Render("proposal") + statusStyle.Render(" · "+orDash(p.RiskLevel)+"  $ ") + proposalCmdStyle.Render(p.Command)
		right := proposalKeyStyle.Render("[y]") + " run  " + proposalKeyStyle.Render("[n]") + " skip"
		pad := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 4
		if pad < 0 {
			pad = 0
		}
		banner = "\n  " + left + strings.Repeat(" ", pad) + right + "\n"
	case m.pendingID != "":
		left := pendingStyle.Render("awaiting approval") + statusStyle.Render("  $ ") + proposalCmdStyle.Render(m.pendingCm)
		right := proposalKeyStyle.Render("[y]") + " run  " + proposalKeyStyle.Render("[n]") + " skip"
		pad := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 4
		if pad < 0 {
			pad = 0
		}
		banner = "\n  " + left + strings.Repeat(" ", pad) + right + "\n"
	case strings.HasPrefix(m.input.Value(), "/"):
		var menuLines []string
		for i, cmd := range m.slashCommands() {
			prefix := "  "
			if i == m.slashMenuIdx {
				prefix = lipgloss.NewStyle().Foreground(colAccent).Render("❯ ")
				menuLines = append(menuLines, "  "+prefix+lipgloss.NewStyle().Foreground(colText).Render(cmd))
			} else {
				menuLines = append(menuLines, "  "+prefix+cmd)
			}
		}
		menuBlock := lipgloss.NewStyle().Foreground(colMuted).Padding(0, 2).Render(strings.Join(menuLines, "\n"))
		banner = "\n" + menuBlock + "\n"
	default:
		banner = "\n"
	}

	// 3. Input Box
	topInp := borderStyle.Render("╭" + strings.Repeat("─", m.width-2) + "╮")
	botInp := borderStyle.Render("╰" + strings.Repeat("─", m.width-2) + "╯")

	inpLineStr := m.input.View()
	padInp := m.width - 4 - lipgloss.Width(inpLineStr)
	if padInp < 0 {
		padInp = 0
	}
	midInp := borderStyle.Render("│ ") + inpLineStr + strings.Repeat(" ", padInp) + borderStyle.Render(" │")

	inputBox := topInp + "\n" + midInp + "\n" + botInp

	// 4. Footer Hint
	hint := "\n" + hintStyle.Render("Enter send · y/n approve · /stop /auto /resume control · ↑↓ history · Ctrl+C quit")

	return timelineStr + banner + inputBox + hint
}

func (m model) renderFramedTimeline(body string) string {
	title := titleStyle.Render("YUMMI")
	sess := "connecting"
	if m.client.SessionID != "" {
		sess = short(m.client.SessionID)
	}

	sessLabel := sessLabelStyle.Render("session")
	sessValue := sessValueStyle.Render(sess)
	dotSep := statusStyle.Render("·")

	status := fmt.Sprintf("%s %s %s", sessLabel, dotSep, sessValue)
	if m.mode != "" {
		modeStyle := modeAskStyle
		label := "ask"
		if m.mode == "auto" {
			modeStyle, label = modeAutoStyle, "auto"
		}
		status += "   " + sessLabelStyle.Render("mode") + " " + dotSep + " " + modeStyle.Render(label)
	}
	status += "   " + m.healthDots()
	if m.activeTaskID != "" {
		status += lipgloss.NewStyle().Foreground(colCyan).Render("  ▶ task")
	}
	if m.loading {
		status += lipgloss.NewStyle().Foreground(colAccent).Render("  · working…")
	}

	leftStr := borderStyle.Render("╭─ ") + title + borderStyle.Render(" ")
	rightStr := "   " + status + borderStyle.Render(" ─╮")

	leftWidth := lipgloss.Width(leftStr)
	rightWidth := lipgloss.Width(rightStr)

	midWidth := m.width - leftWidth - rightWidth
	if midWidth < 0 {
		midWidth = 0
	}
	midStr := borderStyle.Render(strings.Repeat("─", midWidth))

	topBorder := leftStr + midStr + rightStr

	lines := strings.Split(body, "\n")
	for i, line := range lines {
		contentWidth := lipgloss.Width(line)
		pad := m.width - 2 - contentWidth
		if pad < 0 {
			pad = 0
		}
		lines[i] = borderStyle.Render("│") + line + strings.Repeat(" ", pad) + borderStyle.Render("│")
	}
	bottomBorder := borderStyle.Render("╰" + strings.Repeat("─", m.width-2) + "╯")

	return topBorder + "\n" + strings.Join(lines, "\n") + "\n" + bottomBorder
}

func (m model) healthDots() string {
	if len(m.modules) == 0 {
		return ""
	}
	var b strings.Builder
	for _, mod := range m.modules {
		dot := lipgloss.NewStyle().Foreground(colMuted)
		switch mod.Status {
		case "healthy", "connected", "up":
			dot = dot.Foreground(colGreen)
		case "degraded":
			dot = dot.Foreground(colOrange)
		case "down", "unavailable", "not_connected":
			dot = dot.Foreground(colRed)
		}
		fmt.Fprintf(&b, "%s%s ", dot.Render("●"), modLabelStyle.Render(mod.Name))
	}
	return strings.TrimSpace(b.String())
}

func tickHealth() tea.Cmd {
	return tea.Tick(8*time.Second, func(time.Time) tea.Msg { return healthTickMsg{} })
}
