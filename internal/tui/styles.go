package tui

import "github.com/charmbracelet/lipgloss"

var (
	// Colors
	ColorPrimary   = lipgloss.Color("#FF5F87") // Soft pink/red for Yummi
	ColorSecondary = lipgloss.Color("#5FAFFF") // Soft blue
	ColorSuccess   = lipgloss.Color("#00D787") // Soft green
	ColorError     = lipgloss.Color("#FF5F5F") // Error red
	ColorMuted     = lipgloss.Color("#626262") // Gray for dim text
	ColorText      = lipgloss.Color("#EEEEEE") // Standard text
	ColorBgDark    = lipgloss.Color("#1C1C1C") // Dark background

	// Status Bar
	StatusBarStyle = lipgloss.NewStyle().
			Foreground(ColorText).
			Background(lipgloss.Color("#3A3A3A")).
			Padding(0, 1).
			Width(100) // Will be resized

	StatusTitleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FFFFFF")).
				Background(ColorPrimary).
				Padding(0, 1).
				Bold(true)

	StatusOKStyle = lipgloss.NewStyle().Foreground(ColorSuccess).Bold(true)
	StatusErrStyle = lipgloss.NewStyle().Foreground(ColorError).Bold(true)

	// Chat Messages
	UserMessageStyle = lipgloss.NewStyle().
				Foreground(ColorSecondary).
				Bold(true).
				MarginBottom(1)

	AgentMessageStyle = lipgloss.NewStyle().
				Foreground(ColorText).
				Border(lipgloss.NormalBorder(), false, false, false, true).
				BorderForeground(ColorPrimary).
				PaddingLeft(1).
				MarginBottom(1)

	AgentThoughtStyle = lipgloss.NewStyle().
				Foreground(ColorMuted).
				Italic(true).
				PaddingLeft(1).
				MarginBottom(1)

	// Input Area
	InputPromptStyle = lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true)
	InputTextStyle   = lipgloss.NewStyle().Foreground(ColorText)
)
