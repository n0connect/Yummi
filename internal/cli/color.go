package cli

import (
	"fmt"
	"io"
	"os"
	"runtime"
)

// ANSI color/style codes. We disable them when the output is not a terminal or
// on Windows without a VT-capable console (best-effort via env check).
const (
	ansiReset  = "\033[0m"
	ansiBold   = "\033[1m"
	ansiDim    = "\033[2m"
	ansiGreen  = "\033[32m"
	ansiRed    = "\033[31m"
	ansiYellow = "\033[33m"
	ansiCyan   = "\033[36m"
)

func colorsEnabled() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	if runtime.GOOS == "windows" {
		// Only enable on Windows if WT_SESSION is set (Windows Terminal) or CI
		if os.Getenv("WT_SESSION") == "" && os.Getenv("CI") == "" {
			return false
		}
	}
	return true
}

func colorize(code, text string) string {
	if !colorsEnabled() {
		return text
	}
	return code + text + ansiReset
}

func bold(text string) string       { return colorize(ansiBold, text) }
func green(text string) string      { return colorize(ansiGreen, text) }
func red(text string) string        { return colorize(ansiRed, text) }
func yellow(text string) string     { return colorize(ansiYellow, text) }
func cyan(text string) string       { return colorize(ansiCyan, text) }
func dim(text string) string        { return colorize(ansiDim, text) }
func boldCyan(text string) string   { return colorize(ansiBold+ansiCyan, text) }
func boldGreen(text string) string  { return colorize(ansiBold+ansiGreen, text) }
func boldRed(text string) string    { return colorize(ansiBold+ansiRed, text) }
func boldYellow(text string) string { return colorize(ansiBold+ansiYellow, text) }

// stepOK prints a successful preflight check line.
func stepOK(w io.Writer, label, detail string) {
	fmt.Fprintf(w, "  %s %s %s\n", boldGreen("✓"), bold(label), dim(detail))
}

// stepFail prints a failed preflight check line.
func stepFail(w io.Writer, label, detail string) {
	fmt.Fprintf(w, "  %s %s %s\n", boldRed("✗"), bold(label), red(detail))
}

// stepWarn prints a warning preflight check line.
func stepWarn(w io.Writer, label, detail string) {
	fmt.Fprintf(w, "  %s %s %s\n", boldYellow("⚠"), bold(label), yellow(detail))
}

// stepRunning prints a "checking" indicator (no newline held, result follows).
func stepInfo(w io.Writer, label, detail string) {
	fmt.Fprintf(w, "  %s %s %s\n", cyan("▶"), bold(label), dim(detail))
}

// sectionHeader prints a section title.
func sectionHeader(w io.Writer, title string) {
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%s\n", boldCyan("  ══ "+title+" ══"))
}
