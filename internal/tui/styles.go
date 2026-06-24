package tui

import "github.com/charmbracelet/lipgloss"

// Yummi terminal palette — teal accent, matching the web UI (--accent #2dd4bf).
var (
	colAccent = lipgloss.Color("#2dd4bf") // teal — brand
	colCyan   = lipgloss.Color("#38bdf8") // memory / handoff
	colGreen  = lipgloss.Color("#30d158") // success
	colOrange = lipgloss.Color("#ff9f0a") // findings / medium
	colRed    = lipgloss.Color("#ff453a") // error / failed
	colMuted  = lipgloss.Color("#6b7280") // dim text
	colText   = lipgloss.Color("#e5e7eb") // primary text
	colInk    = lipgloss.Color("#04201c") // text on accent fills
)

var (
	titleStyle  = lipgloss.NewStyle().Foreground(colInk).Background(colAccent).Bold(true).Padding(0, 1)
	statusStyle = lipgloss.NewStyle().Foreground(colMuted)

	userStyle   = lipgloss.NewStyle().Foreground(colCyan).Bold(true)
	agentStyle  = lipgloss.NewStyle().Foreground(colText).Border(lipgloss.NormalBorder(), false, false, false, true).BorderForeground(colAccent).PaddingLeft(1)
	systemStyle = lipgloss.NewStyle().Foreground(colMuted).Italic(true)

	hintStyle      = lipgloss.NewStyle().Foreground(colMuted)
	proposalStyle  = lipgloss.NewStyle().Foreground(colOrange).Bold(true)
	pendingStyle   = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	inputPrompt    = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	inputTextStyle = lipgloss.NewStyle().Foreground(colText)

	execOKStyle  = lipgloss.NewStyle().Foreground(colGreen)
	execErrStyle = lipgloss.NewStyle().Foreground(colRed)
	cmdStyle     = lipgloss.NewStyle().Foreground(colText)
	outputStyle  = lipgloss.NewStyle().Foreground(colMuted)
)

// chipStyle returns the lipgloss style for an activity chip of the given kind.
func chipStyle(kind string) lipgloss.Style {
	switch kind {
	case "memory_recalled", "handoff_started", "handoff_completed":
		return lipgloss.NewStyle().Foreground(colCyan)
	case "finding":
		return lipgloss.NewStyle().Foreground(colOrange)
	case "skill_applied", "context_saved":
		return lipgloss.NewStyle().Foreground(colAccent)
	default:
		return lipgloss.NewStyle().Foreground(colMuted)
	}
}
