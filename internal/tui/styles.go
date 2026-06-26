package tui

import "github.com/charmbracelet/lipgloss"

// Yummi terminal palette — premium, high-contrast dark theme colors.
var (
	colAccent = lipgloss.Color("#00e5ff") // vibrant cyan — brand accent
	colCyan   = lipgloss.Color("#38bdf8") // soft cyan/blue — activity/command
	colGreen  = lipgloss.Color("#30d158") // success green — online status
	colOrange = lipgloss.Color("#ff9f0a") // warm orange — proposals/attention
	colRed    = lipgloss.Color("#ff453a") // alert red — errors
	colMuted  = lipgloss.Color("#6b7280") // dim gray — metadata/system
	colText   = lipgloss.Color("#e5e7eb") // ice white — primary reading text
	colInk    = lipgloss.Color("#000000") // pure black — badge contrast
)

var (
	titleStyle  = lipgloss.NewStyle().Foreground(colText).Bold(true)
	statusStyle = lipgloss.NewStyle().Foreground(colMuted)

	// Header elements
	sessLabelStyle = lipgloss.NewStyle().Foreground(colMuted)
	sessValueStyle = lipgloss.NewStyle().Foreground(colText)
	modLabelStyle  = lipgloss.NewStyle().Foreground(colText)
	borderStyle    = lipgloss.NewStyle().Foreground(colMuted)

	// Timeline elements
	userStyle   = lipgloss.NewStyle().Foreground(colText)
	agentStyle  = lipgloss.NewStyle().Foreground(colText).Border(lipgloss.NormalBorder(), false, false, false, true).BorderForeground(colAccent).PaddingLeft(1)
	systemStyle = lipgloss.NewStyle().Foreground(colMuted).Italic(true)

	hintStyle          = lipgloss.NewStyle().Foreground(colMuted).PaddingLeft(2)
	proposalCmdStyle   = lipgloss.NewStyle().Foreground(colText).Bold(true)
	proposalKeyStyle   = lipgloss.NewStyle().Foreground(colText).Bold(true)
	proposalLabelStyle = lipgloss.NewStyle().Foreground(colOrange).Bold(true)

	pendingStyle   = lipgloss.NewStyle().Foreground(colInk).Background(colAccent).Bold(true).Padding(0, 1)
	inputPrompt    = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	inputTextStyle = lipgloss.NewStyle().Foreground(colText)

	// Command execution elements
	execOKStyle  = lipgloss.NewStyle().Foreground(colGreen)
	execErrStyle = lipgloss.NewStyle().Foreground(colRed)
	cmdStyle     = lipgloss.NewStyle().Foreground(colText)
	outputStyle  = lipgloss.NewStyle().Foreground(colMuted).Border(lipgloss.NormalBorder(), false, false, false, true).BorderForeground(colMuted).PaddingLeft(1)

	// Welcome Screen
	welcomeLogoStyle = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	welcomeSubStyle  = lipgloss.NewStyle().Foreground(colMuted)

	// Plan rendering
	planStepStyle = lipgloss.NewStyle().Foreground(colText)

	// Approval-mode header indicator: auto = looser (orange), ask = safer (green).
	modeAutoStyle = lipgloss.NewStyle().Foreground(colOrange).Bold(true)
	modeAskStyle  = lipgloss.NewStyle().Foreground(colGreen).Bold(true)
)

// Badge styles for activity chips.
var (
	badgeRecalled   = lipgloss.NewStyle().Foreground(colInk).Background(colCyan).Bold(true).Padding(0, 1)
	badgeSkill      = lipgloss.NewStyle().Foreground(colInk).Background(colAccent).Bold(true).Padding(0, 1)
	badgeFinding    = lipgloss.NewStyle().Foreground(colInk).Background(colOrange).Bold(true).Padding(0, 1)
	badgeDelegated  = lipgloss.NewStyle().Foreground(colInk).Background(colCyan).Bold(true).Padding(0, 1)
	badgeReturned   = lipgloss.NewStyle().Foreground(colInk).Background(colMuted).Bold(true).Padding(0, 1)
	badgeRemembered = lipgloss.NewStyle().Foreground(colInk).Background(colGreen).Bold(true).Padding(0, 1)
	badgePlan       = lipgloss.NewStyle().Foreground(colInk).Background(colOrange).Bold(true).Padding(0, 1)
	badgeAsk        = lipgloss.NewStyle().Foreground(colInk).Background(colOrange).Bold(true).Padding(0, 1)
	badgeDone       = lipgloss.NewStyle().Foreground(colInk).Background(colGreen).Bold(true).Padding(0, 1)
)

func getBadgeStyle(kind string) lipgloss.Style {
	switch kind {
	case "memory_recalled":
		return badgeRecalled
	case "skill_applied":
		return badgeSkill
	case "finding":
		return badgeFinding
	case "handoff_started":
		return badgeDelegated
	case "handoff_completed":
		return badgeReturned
	case "context_saved":
		return badgeRemembered
	case "plan":
		return badgePlan
	case "ask":
		return badgeAsk
	case "finish":
		return badgeDone
	default:
		return lipgloss.NewStyle().Foreground(colInk).Background(colMuted).Bold(true).Padding(0, 1)
	}
}
