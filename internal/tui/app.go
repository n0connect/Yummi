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

// model is the TUI operator console state.
type model struct {
	client *Client
	base   string
	stream chan tea.Msg

	viewport viewport.Model
	input    textinput.Model

	lines     []string   // rendered timeline lines
	proposals []Proposal // active single-command proposals (first is acted on)
	pendingID string     // agent task awaiting approval
	pendingPr string     // its proposal id
	pendingCm string     // its command (banner)

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
	ti.Focus()
	ti.CharLimit = 500
	ti.Prompt = "❯ "
	ti.PromptStyle = inputPrompt
	ti.TextStyle = inputTextStyle

	m := model{
		client: NewClient(apiURL),
		base:   apiURL,
		input:  ti,
		lines:  []string{renderSystem("Yummi terminal console — connecting…")},
	}
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		textinput.Blink,
		func() tea.Msg { return sessionCreatedMsg{err: m.client.CreateSession()} },
		func() tea.Msg { return healthMsg{mods: m.client.Health()} },
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
		vpHeight := msg.Height - 4 // header(1)+banner/input(2)+hint(1)
		if vpHeight < 1 {
			vpHeight = 1
		}
		if !m.ready {
			m.viewport = viewport.New(msg.Width, vpHeight)
			m.ready = true
		} else {
			m.viewport.Width, m.viewport.Height = msg.Width, vpHeight
		}
		m.refresh()

	case sessionCreatedMsg:
		if msg.err != nil {
			m.append(renderSystem("session init failed: " + msg.err.Error()))
		} else {
			m.append(renderSystem(fmt.Sprintf("session active [%s] — ready", short(m.client.SessionID))))
			m.stream = newStream(m.base, m.client.SessionID)
			cmds = append(cmds, waitForEvent(m.stream), tickHealth())
		}

	case chatResultMsg:
		m.loading = false
		if msg.err != nil {
			m.append(renderSystem("error: " + msg.err.Error()))
			break
		}
		if r := strings.TrimSpace(msg.res.Reply); r != "" {
			m.append(renderAgent(r))
		}
		if len(msg.res.Proposals) > 0 {
			m.proposals = msg.res.Proposals
		} else if msg.res.TaskID != "" {
			m.append(renderSystem("task started — watching…"))
		}

	case execMsg:
		m.append(renderExec(msg.Command, msg.Status, firstNonEmpty(msg.Output, msg.Error)))
		cmds = append(cmds, waitForEvent(m.stream))

	case taskMsg:
		m.onTask(msg)
		cmds = append(cmds, waitForEvent(m.stream))

	case activityMsg:
		if s := renderActivity(msg.Kind, msg.Summary, msg.Parent != ""); s != "" {
			m.append(s)
		}
		cmds = append(cmds, waitForEvent(m.stream))

	case wsErrMsg:
		m.append(renderSystem(msg.note))
		cmds = append(cmds, waitForEvent(m.stream))

	case approveDoneMsg:
		if msg.err != nil {
			m.append(renderSystem("approve failed: " + msg.err.Error()))
		}

	case healthMsg:
		m.modules = msg.mods

	case healthTickMsg:
		cmds = append(cmds, func() tea.Msg { return healthMsg{mods: m.client.Health()} }, tickHealth())
	}

	var c tea.Cmd
	m.input, c = m.input.Update(msg)
	cmds = append(cmds, c)
	m.viewport, c = m.viewport.Update(msg)
	cmds = append(cmds, c)
	return m, tea.Batch(cmds...)
}

// handleKey processes keys that the console consumes; returns handled=true to
// short-circuit the normal input/viewport update.
func (m *model) handleKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.Type {
	case tea.KeyCtrlC, tea.KeyEsc:
		return tea.Quit, true
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
		return m.sendChat(), true
	case tea.KeyRunes:
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
					m.append(renderSystem("proposal dismissed"))
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

func (m *model) sendChat() tea.Cmd {
	text := strings.TrimSpace(m.input.Value())
	m.input.Reset()
	m.proposals = nil
	m.append(renderUser(text))
	m.loading = true
	return func() tea.Msg {
		res, err := m.client.Chat(text)
		return chatResultMsg{res: res, err: err}
	}
}

func (m *model) approveProposal() tea.Cmd {
	p := m.proposals[0]
	m.proposals = nil
	m.append(renderSystem("running: " + p.Command))
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
	m.append(renderSystem("command " + verb))
	return func() tea.Msg { return approveDoneMsg{err: m.client.ApproveTask(taskID, propID, approved)} }
}

func (m *model) onTask(t taskMsg) {
	switch t.Status {
	case "awaiting_approval":
		if t.PendingCommand != "" {
			m.pendingID, m.pendingPr, m.pendingCm = t.ID, t.PendingPropID, t.PendingCommand
		}
	case "paused":
		if t.Question != "" {
			m.append(renderSystem("agent asks: " + t.Question))
		}
	case "completed", "failed", "stopped":
		if t.Summary != "" {
			m.append(renderAgent(t.Summary))
		}
		for _, f := range t.Findings {
			m.append(renderActivity("finding", f, false))
		}
		m.pendingID, m.pendingPr, m.pendingCm = "", "", ""
	}
}

func (m *model) append(line string) {
	m.lines = append(m.lines, line)
	m.refresh()
}

func (m *model) refresh() {
	if !m.ready {
		return
	}
	m.viewport.SetContent(strings.Join(m.lines, "\n"))
	m.viewport.GotoBottom()
}

func (m model) View() string {
	if !m.ready {
		return "\n  Initializing…"
	}
	header := m.renderHeader()
	body := m.viewport.View()

	var banner string
	switch {
	case len(m.proposals) > 0:
		p := m.proposals[0]
		banner = proposalStyle.Render(fmt.Sprintf("proposal [%s] $ %s", orDash(p.RiskLevel), p.Command))
	case m.pendingID != "":
		banner = pendingStyle.Render("awaiting approval $ " + m.pendingCm)
	}

	footer := m.input.View()
	hint := hintStyle.Render("Enter send · y/n approve/reject · ↑↓ scroll · Ctrl+C quit")

	parts := []string{header, body}
	if banner != "" {
		parts = append(parts, banner)
	}
	parts = append(parts, footer, hint)
	return strings.Join(parts, "\n")
}

func (m model) renderHeader() string {
	title := titleStyle.Render(" YUMMI ")
	sess := "connecting"
	if m.client.SessionID != "" {
		sess = short(m.client.SessionID)
	}
	status := statusStyle.Render(fmt.Sprintf(" session %s  %s", sess, m.healthDots()))
	if m.loading {
		status += lipgloss.NewStyle().Foreground(colAccent).Render("  · working…")
	}
	gap := m.width - lipgloss.Width(title) - lipgloss.Width(status)
	if gap < 0 {
		gap = 0
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, title, status, strings.Repeat(" ", gap))
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
		fmt.Fprintf(&b, "%s%s ", dot.Render("●"), mod.Name)
	}
	return strings.TrimSpace(b.String())
}

func tickHealth() tea.Cmd {
	return tea.Tick(8*time.Second, func(time.Time) tea.Msg { return healthTickMsg{} })
}
