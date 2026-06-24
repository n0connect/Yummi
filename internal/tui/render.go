package tui

import "strings"

// activityMeta maps an activity kind to its [icon, label]. Mirrors the web
// AGENT timeline. command/command_result render via the exec line, not here.
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

func renderUser(text string) string   { return userStyle.Render("► " + text) }
func renderAgent(text string) string  { return agentStyle.Render(strings.TrimSpace(text)) }
func renderSystem(text string) string { return systemStyle.Render("[system] " + text) }

// renderActivity formats one activity chip, or "" for kinds shown elsewhere
// (command/command_result render via renderExec). nested events are indented.
func renderActivity(kind, summary string, nested bool) string {
	meta, ok := activityMeta[kind]
	if !ok {
		return "" // command / command_result / unknown → not a chip
	}
	indent := ""
	if nested {
		indent = "  └ "
	}
	line := meta[0] + " " + meta[1] + " · " + truncate(summary, 80)
	return indent + chipStyle(kind).Render(line)
}

// renderExec formats a finished command result line.
func renderExec(command, status, output string) string {
	mark, style := "✓", execOKStyle
	if status != "completed" {
		mark, style = "✗", execErrStyle
	}
	head := style.Render(mark) + " " + cmdStyle.Render(command)
	out := strings.TrimSpace(output)
	if out == "" {
		return head
	}
	return head + "\n" + outputStyle.Render(indentLines(truncateLines(out, 12), "  "))
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
