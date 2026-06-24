package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type errMsg error

type message struct {
	role    string
	content string
}

type chatMsg struct {
	content string
}

type initSessionMsg struct{}
type sessionCreatedMsg struct{ err error }

type model struct {
	client       *APIClient
	viewport     viewport.Model
	textInput    textinput.Model
	messages     []message
	err          error
	ready        bool
	loading      bool
	width        int
	height       int
}

// Run starts the Bubble Tea TUI program
func Run(apiURL string) error {
	ti := textinput.New()
	ti.Placeholder = "Tell Yummi what to do..."
	ti.Focus()
	ti.CharLimit = 500
	ti.Width = 60
	ti.PromptStyle = InputPromptStyle
	ti.TextStyle = InputTextStyle

	m := model{
		client:    NewAPIClient(apiURL),
		textInput: ti,
		messages:  []message{{role: "system", content: "Welcome to Yummi TUI. Initializing session..."}},
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return err
	}
	return nil
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		textinput.Blink,
		func() tea.Msg { return initSessionMsg{} },
	)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		tiCmd tea.Cmd
		vpCmd tea.Cmd
	)

	m.textInput, tiCmd = m.textInput.Update(msg)
	m.viewport, vpCmd = m.viewport.Update(msg)

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			return m, tea.Quit
		case tea.KeyEnter:
			if m.loading {
				return m, nil
			}
			v := m.textInput.Value()
			if v == "" {
				return m, nil
			}
			m.messages = append(m.messages, message{role: "user", content: v})
			m.textInput.Reset()
			m.loading = true
			m.updateViewport()
			
			// Send to API
			return m, func() tea.Msg {
				reply, err := m.client.SendChat(v)
				if err != nil {
					return errMsg(err)
				}
				return chatMsg{content: reply}
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		headerHeight := 2
		footerHeight := 2
		vpHeight := msg.Height - headerHeight - footerHeight

		if !m.ready {
			m.viewport = viewport.New(msg.Width, vpHeight)
			m.ready = true
		} else {
			m.viewport.Width = msg.Width
			m.viewport.Height = vpHeight
		}
		m.updateViewport()

	case initSessionMsg:
		return m, func() tea.Msg {
			err := m.client.CreateSession()
			return sessionCreatedMsg{err: err}
		}

	case sessionCreatedMsg:
		if msg.err != nil {
			m.err = msg.err
			m.messages = append(m.messages, message{role: "system", content: fmt.Sprintf("Failed to init: %v", msg.err)})
		} else {
			m.messages = append(m.messages, message{role: "system", content: fmt.Sprintf("Session active [%s]. Ready for commands.", m.client.SessionID)})
		}
		m.updateViewport()

	case chatMsg:
		m.loading = false
		m.messages = append(m.messages, message{role: "agent", content: msg.content})
		m.updateViewport()

	case errMsg:
		m.loading = false
		m.err = msg
		m.messages = append(m.messages, message{role: "system", content: fmt.Sprintf("Error: %v", msg)})
		m.updateViewport()
	}

	return m, tea.Batch(tiCmd, vpCmd)
}

func (m *model) updateViewport() {
	var b strings.Builder
	for _, msg := range m.messages {
		switch msg.role {
		case "user":
			b.WriteString(UserMessageStyle.Render("► " + msg.content))
		case "agent":
			// Very basic split for <THOUGHT> vs normal text if they exist
			content := msg.content
			if strings.Contains(content, "<THOUGHT>") {
				parts := strings.SplitN(content, "</THOUGHT>", 2)
				if len(parts) == 2 {
					thought := strings.Replace(parts[0], "<THOUGHT>", "", 1)
					b.WriteString(AgentThoughtStyle.Render(strings.TrimSpace(thought)))
					b.WriteString("\n")
					b.WriteString(AgentMessageStyle.Render(strings.TrimSpace(parts[1])))
				} else {
					b.WriteString(AgentMessageStyle.Render(content))
				}
			} else {
				b.WriteString(AgentMessageStyle.Render(content))
			}
		case "system":
			b.WriteString(AgentThoughtStyle.Render("[System] " + msg.content))
		}
		b.WriteString("\n")
	}
	m.viewport.SetContent(b.String())
	m.viewport.GotoBottom()
}

func (m model) View() string {
	if !m.ready {
		return "\n  Initializing..."
	}

	// Status Bar
	sessionStr := m.client.SessionID
	if sessionStr == "" {
		sessionStr = "Connecting..."
	}
	if len(sessionStr) > 8 {
		sessionStr = sessionStr[:8]
	}

	title := StatusTitleStyle.Render(" YUMMI TUI ")
	status := fmt.Sprintf(" Session: %s | Orchestrator: %s", sessionStr, "http://localhost:9080")
	if m.loading {
		status += " | " + lipgloss.NewStyle().Foreground(ColorPrimary).Render("Processing...")
	}

	headerPadding := m.width - lipgloss.Width(title) - lipgloss.Width(status)
	if headerPadding < 0 {
		headerPadding = 0
	}
	header := lipgloss.JoinHorizontal(lipgloss.Top,
		title,
		StatusBarStyle.Copy().Width(m.width-lipgloss.Width(title)).Render(status),
	)

	// Body
	body := m.viewport.View()

	// Footer (Input)
	footer := m.textInput.View()

	return fmt.Sprintf("%s\n%s\n%s", header, body, footer)
}
