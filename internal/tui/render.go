package tui

import (
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

// activityMeta maps an activity kind to its [icon, label].
var activityMeta = map[string][2]string{
	"memory_recalled":   {"🧠", "recalled"},
	"skill_applied":     {"📘", "skill"},
	"finding":           {"🔎", "finding"},
	"handoff_started":   {"🤝", "delegated"},
	"handoff_completed": {"↩", "returned"},
	"context_saved":     {"💾", "remembered"},
	"plan":              {"🗺", "plan"},
	"ask":               {"❓", "ask"},
	"finish":            {"🏁", "done"},
}

// ── Timeline Items ──────────────────────────────────────────────────────────

type TimelineItem interface {
	Render(width int) string
}

type UserItem struct {
	Text string
}

func (u UserItem) Render(width int) string {
	w := width - 8
	if w < 10 {
		w = 80
	}
	s := userStyle.Width(w)
	return "  > " + s.Render(u.Text)
}

type AgentItem struct {
	Text string
}

func (a AgentItem) Render(width int) string {
	w := width - 8
	if w < 10 {
		w = 80
	}

	tr, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(w),
	)
	var out string
	if err == nil {
		out, _ = tr.Render(strings.TrimSpace(a.Text))
	} else {
		out = strings.TrimSpace(a.Text)
	}

	out = strings.TrimSuffix(out, "\n")
	return indentLines(agentStyle.Render(out), "  ")
}

type SystemItem struct {
	Text string
}

func (s SystemItem) Render(width int) string {
	return "  " + systemStyle.Render("[system] "+s.Text)
}

type ActivityItem struct {
	Kind    string
	Summary string
	Nested  bool
}

func (a ActivityItem) Render(width int) string {
	meta, ok := activityMeta[a.Kind]
	if !ok {
		return ""
	}
	indent := ""
	if a.Nested {
		indent = "  └ "
	}
	badge := getBadgeStyle(a.Kind).Render(meta[0] + " " + meta[1])
	details := statusStyle.Render(" · " + truncate(a.Summary, 80))
	return "  " + indent + badge + details
}

type ExecItem struct {
	Command string
	Status  string
	Output  string
	// Banner is the orchestrator's threat-scan warning (execution.SecurityBanner)
	// when tool output looked like a content-layer injection attempt — shown to
	// the operator the same way AssistantPanel.tsx's "execution-threat" alert does.
	Banner string
}

func (e ExecItem) Render(width int) string {
	mark, style := "✓", execOKStyle
	if e.Status != "completed" {
		mark, style = "✗", execErrStyle
	}
	head := "  " + style.Render(mark) + " " + cmdStyle.Render(e.Command)
	if banner := strings.TrimSpace(e.Banner); banner != "" {
		head += "\n  " + execErrStyle.Render("⚠ "+banner)
	}
	out := strings.TrimSpace(e.Output)
	if out == "" {
		return head
	}

	// Convert tabs to spaces to prevent misalignment in lipgloss/terminal
	out = strings.ReplaceAll(out, "\t", "    ")

	renderedOut := outputStyle.Render(truncateLines(out, 12))
	return head + "\n" + indentLines(renderedOut, "  ")
}

// PlanItem renders the agent's plan — the LLM's primary planning artifact — as a
// titled, step-by-step block so the operator can see what the agent intends to
// do and (via per-step glyphs) how far it has progressed.
type PlanItem struct {
	Goal  string
	Steps []PlanStep
}

func (p PlanItem) Render(width int) string {
	if len(p.Steps) == 0 {
		return ""
	}
	header := "  " + badgePlan.Render("🗺 plan")
	if g := strings.TrimSpace(p.Goal); g != "" {
		header += statusStyle.Render(" · " + truncate(g, 70))
	}
	lines := []string{header}
	for _, s := range p.Steps {
		glyph, gstyle := stepGlyph(s.Status)
		desc := strings.TrimSpace(s.Description)
		line := "    " + gstyle.Render(glyph) + " " + planStepStyle.Render(truncate(desc, 80))
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// stepGlyph maps a plan-step status to its marker glyph and style.
func stepGlyph(status string) (string, lipgloss.Style) {
	switch status {
	case "done":
		return "✓", execOKStyle
	case "running":
		return "▶", lipgloss.NewStyle().Foreground(colCyan).Bold(true)
	case "failed":
		return "✗", execErrStyle
	case "skipped":
		return "⊘", statusStyle
	default: // pending
		return "○", statusStyle
	}
}

type RawItem struct {
	Text string
}

func (r RawItem) Render(width int) string {
	return r.Text
}

// ── small utils ──────────────────────────────────────────────────────────────

func short(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func truncateLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "\n") + "\n…"
}

func indentLines(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}
