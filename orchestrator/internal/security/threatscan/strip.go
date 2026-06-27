package threatscan

import "regexp"

// ansiEscape matches ANSI / ECMA-48 escape sequences emitted by interactive CLI
// tools (nmap, gobuster, colored shells). Covered forms:
//   - CSI: ESC [ <params> <intermediates> <final>   (colors, cursor moves)
//   - OSC: ESC ] ... (BEL | ST)                      (window-title sets)
//   - DCS/SOS/PM/APC string sequences: ESC P/X/^/_ ... ST
//   - nF / Fp-Fs single- and multi-byte escapes
//   - 8-bit C1 forms (0x9B CSI, 0x9D OSC) and lone C1 control bytes (0x80–0x9F)
//
// (?s) lets the string-terminated forms (OSC/DCS) span newlines. The pattern is
// built from explicit byte ranges so it stays auditable.
// High codepoints (0x9B, 0x9D, the C1 range) are written as regex \x{..} rune
// escapes, not literal bytes: a literal 0x9B byte in the pattern string is
// invalid UTF-8 and regexp rejects it. The 7-bit ESC (\x1b) and BEL (\x07) are
// valid single bytes and stay literal. The 7-bit ESC forms cover virtually all
// real CLI output; the 8-bit C1 forms are kept for completeness.
var ansiEscape = regexp.MustCompile("(?s)" +
	"\x1b(?:" +
	"\\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]" + // CSI
	"|\\][^\x07\x1b]*(?:\x07|\x1b\\\\)" + // OSC (BEL or ST terminator)
	"|[PX^_].*?\x1b\\\\" + // DCS/SOS/PM/APC string ... ST
	"|[\x20-\x2f]+[\x30-\x7e]" + // nF multi-byte escape
	"|[\x30-\x7e]" + // Fp/Fe/Fs single-byte escape
	")" +
	"|\\x{9b}[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]" + // 8-bit CSI
	"|\\x{9d}[^\x07\\x{9c}]*(?:\x07|\\x{9c})" + // 8-bit OSC (BEL or ST)
	"|[\\x{80}-\\x{9f}]") // lone C1 control bytes

// hasEscape is a cheap gate so clean text skips the full regex.
var hasEscape = regexp.MustCompile("[\x1b\\x{80}-\\x{9f}]")

// StripANSI removes ANSI/C1 escape sequences from tool output before it enters
// the LLM prompt and the operator's observation log. Two reasons it matters for
// a pentest conductor:
//
//  1. Hygiene — colored/cursor-control output pollutes the model's context and
//     leads weak models to echo escape bytes back into commands and file writes.
//  2. Safety — escape sequences are a terminal/log-injection vector (a malicious
//     target can emit OSC/CSI to spoof log lines or manipulate a terminal that
//     later renders the output). threatscan.Scan catches invisible unicode; this
//     handles the escape-sequence family.
//
// Clean text (no ESC / C1 byte) returns unchanged via the fast path. The raw
// bytes are untouched in the execution store's audit record — only the agent's
// observation copy is sanitized.
func StripANSI(s string) string {
	if s == "" || !hasEscape.MatchString(s) {
		return s
	}
	return ansiEscape.ReplaceAllString(s, "")
}
