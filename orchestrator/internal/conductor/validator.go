package conductor

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// jsonArtifactPattern catches JSON key-value noise that qwen/llama models
// sometimes embed inside the command string instead of as separate JSON fields.
// Example: "nmap 127.0.0.1','rationale':'...','risk_level':'low'"
var jsonArtifactPattern = regexp.MustCompile(`['"]\s*,\s*['"]?(rationale|risk_level|requires_approval|target_tool|id|confidence)['"?\s]*:.*$`)

// sanitizeCommand removes JSON metadata that a model may accidentally embed
// inside the command string. It only unwraps a quote pair that surrounds the
// entire command; blindly trimming quote characters corrupts valid commands
// such as `echo "ready"` into an unterminated shell string.
func sanitizeCommand(cmd string) string {
	cmd = jsonArtifactPattern.ReplaceAllString(cmd, "")
	cmd = strings.TrimSpace(cmd)
	if len(cmd) >= 2 && ((cmd[0] == '"' && cmd[len(cmd)-1] == '"') || (cmd[0] == '\'' && cmd[len(cmd)-1] == '\'')) {
		cmd = strings.TrimSpace(cmd[1 : len(cmd)-1])
	}
	return cmd
}

// Validator enforces risk classification and approval gating for assistant
// proposals. It no longer makes scope decisions — gateway is the sole
// authority on whether a target is reachable.
type Validator struct{}

// NewValidator creates a Validator. Validator no longer makes scope
// decisions — gateway is the sole authority on whether a target is
// reachable. Validator now only classifies and gates on risk level.
func NewValidator() *Validator {
	return &Validator{}
}

// ValidateChatResponse normalizes proposals and classifies risk. It does not
// make scope decisions — gateway is the only component that knows whether a
// target is reachable; that's discovered at execution time
// (executor.RunScopedCommand), not here.
func (v *Validator) ValidateChatResponse(resp ChatResponse) ChatResponse {
	resp.Message = sanitizeVisibleModelText(resp.Message)
	resp.TaskGoal = sanitizeVisibleModelText(resp.TaskGoal)
	for i := range resp.Proposals {
		resp.Proposals[i].Rationale = sanitizeVisibleModelText(resp.Proposals[i].Rationale)
	}

	if v == nil || len(resp.Proposals) == 0 {
		return resp
	}

	validated := make([]Proposal, 0, len(resp.Proposals))
	warnings := append([]string{}, resp.Warnings...)

	for _, p := range resp.Proposals {
		cmd := sanitizeCommand(p.Command)
		if cmd == "" {
			warnings = append(warnings, "empty proposal command was dropped")
			continue
		}
		p.Command = cmd

		tool := firstToken(cmd)

		risk := classifyRisk(cmd)
		p.RiskLevel = maxRisk(p.RiskLevel, risk)

		if destRisk, label := destructiveFloor(cmd); label != "" {
			p.RiskLevel = maxRisk(p.RiskLevel, destRisk)
			p.ForceApprovalReason = "destructive pattern detected: " + label
			warnings = append(warnings, fmt.Sprintf("forced approval — %s: %s", p.ForceApprovalReason, tool))
		}

		p.RequiresApproval = requiresApproval(p.RiskLevel)

		if IsHighSeverity(p.RiskLevel) {
			warnings = append(warnings, fmt.Sprintf("%s-risk proposal requires explicit approval: %s", p.RiskLevel, tool))
		}
		validated = append(validated, p)
	}

	resp.Proposals = validated
	resp.Warnings = uniqStrings(warnings)

	if len(validated) == 0 {
		resp.Intent = "ask_clarification"
		if strings.TrimSpace(resp.Message) == "" {
			resp.Message = "No safe proposal could be prepared. Please share the desired action."
		}
		if resp.Confidence > 0.5 {
			resp.Confidence = 0.5
		}
	}

	return resp
}

var (
	thinkBlockPattern    = regexp.MustCompile(`(?is)<think\b[^>]*>.*?</think\s*>`)
	unclosedThinkPattern = regexp.MustCompile(`(?is)<think\b[^>]*>.*$`)
	internalTagPattern   = regexp.MustCompile(`(?m)^\s*\[(RUN_COMMAND|UPDATE_PLAN|FINISH|HANDOFF|REMEMBER|ASK_OPERATOR|SEND_FULL|CONTINUE|ABORT|STOP_TASK)\]\s*$`)
)

// sanitizeVisibleModelText is the final trust boundary for model-authored text
// that may reach an operator UI. Structured agent protocol, hidden reasoning,
// and pathological unbounded fields stay server-side.
func sanitizeVisibleModelText(text string) string {
	text = thinkBlockPattern.ReplaceAllString(text, "")
	text = unclosedThinkPattern.ReplaceAllString(text, "")
	text = internalTagPattern.ReplaceAllString(text, "")
	text = strings.TrimSpace(text)
	const maxVisibleModelText = 16 * 1024
	runes := []rune(text)
	if len(runes) > maxVisibleModelText {
		text = string(runes[:maxVisibleModelText]) + "\n…"
	}
	return text
}

func firstToken(cmd string) string {
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return ""
	}
	return strings.ToLower(fields[0])
}

// simpleCommandPattern allowlists the structural shape of a single,
// unchained tool invocation: a bare command/path token, followed by
// whitespace-separated argument tokens. Each argument token is one or more
// concatenated fragments — a quoted string or an unquoted run of characters
// that have no meaning to a shell (letters, digits, and a fixed set of
// common flag/path/URL punctuation) — which is what lets ordinary partial
// quoting like cut -d':' -f1 or awk -F':' match: -d':' is the bareword
// fragment "-d" directly followed by the quoted fragment "':'", still one
// shell word with no unquoted shell metacharacter in it.
// Notably ABSENT from the unquoted charset: ; | & ` $ ( ) { } [ ] < > # \ and
// any whitespace other than plain space/tab — every one of those is
// shell-significant (chaining, substitution, redirection, backgrounding,
// globbing) and would change what actually executes inside the sandbox's
// `bash -lc`. A command line containing any of them outside quotes simply
// does not match this shape.
//
// Single- and double-quoted content are NOT equally inert, though, so they
// get different allowed-character sets: single quotes suppress every form of
// expansion in bash, so any byte except the quote char/backslash/CR/LF is
// genuinely inert data. Double quotes still let $VAR expansion and $()/
// backtick command substitution through — `echo "$(cat /etc/passwd)"` runs
// the embedded command — so the double-quoted branch additionally excludes
// `$` and backtick; a double-quoted fragment containing either doesn't match
// this shape and falls through to critical, same as if it were unquoted.
//
// This is a deliberate default-deny allowlist, not a list of forbidden
// characters: classifyRisk does not ask "does this command contain a bad
// character," it asks "does this command match the one shape we understand
// well enough to call low risk." Anything else — including command
// structures we didn't anticipate — falls through to critical.
var simpleCommandPattern = regexp.MustCompile(
	"^[A-Za-z0-9_./-]+(?:[ \t]+(?:\"[^\"$`\\\\\r\n]*\"|'[^'\\\\\r\n]*'|[A-Za-z0-9_.\\-/:=,@%+~?*]+)+)*[ \t]*$",
)

// classifyRisk returns a floor risk level based purely on structural command
// shape — no tool-specific flag knowledge. The model's own risk_level field
// is the primary source of truth; this function only provides a conservative
// safety floor: "low" is granted only to commands matching the simple,
// unchained shape above; a chained-but-all-local-file-processing shape (see
// isSafeLocalPipeline) is floored at "medium", never "low" — FuzzClassifyRisk
// asserts "low" never coincides with an unquoted shell metacharacter, and a
// pipeline necessarily contains one (&&/;/|) by definition, so "medium" is
// both the honest calibration (elevated structural complexity, but every
// named program individually vetted — not the simplest possible shape,
// not maximally dangerous either) and the one that doesn't fight that
// invariant. Everything else — including command structures this function
// doesn't recognize at all — is "critical" by default.
//
// Note for callers: in ask_permission mode, approval gating is mode-driven,
// not risk-driven (agent/node_tool.go, eino/pentest_tools.go's
// runToolPipeline) — EVERY proposal there requires an operator click
// regardless of what this function returns. What this function's accuracy
// actually buys is the risk BADGE shown at that approval prompt and in the
// audit trail: a chained-but-harmless pipeline mislabeled "critical" is
// alert-fatigue-inducing noise that erodes trust in the badge for commands
// that are genuinely critical, not fewer approval clicks.
func classifyRisk(cmd string) string {
	if simpleCommandPattern.MatchString(cmd) {
		return "low"
	}
	if isSafeLocalPipeline(cmd) {
		return "medium"
	}
	return "critical"
}

// safeLocalBinaries is the set of programs a chained pipeline's segments may
// invoke to qualify for isSafeLocalPipeline's floor below. Deliberately
// excludes anything that can itself execute arbitrary code via its own
// arguments even though it's common in real recon pipelines: awk (its
// system() function runs a shell command), sed (GNU sed's "e" command/flag
// does the same), find (-exec/-delete), xargs, env, eval, and any shell or
// language interpreter. Those stay on classifyRisk's existing critical-by-
// default floor unchanged — this list only covers programs with no such
// escape hatch.
var safeLocalBinaries = map[string]bool{
	"cat": true, "sort": true, "uniq": true, "grep": true, "egrep": true, "fgrep": true,
	"rg": true, "wc": true, "head": true, "tail": true, "echo": true, "cut": true,
	"tr": true, "jq": true, "tee": true, "basename": true, "dirname": true,
	"mkdir": true, "touch": true, "ls": true, "pwd": true, "cd": true, "which": true,
	"true": true, "false": true, "date": true, "stat": true, "file": true,
	"md5sum": true, "sha1sum": true, "sha256sum": true,
	// Read-only system/network info display — same no-escape-hatch bar as the
	// set above (no -exec, no embedded interpreter, no arbitrary-code
	// argument), just a different domain (local host/network state instead of
	// local files). Live-observed false positives this closes: an operator
	// running Yummi with a capable model under auto-approve had to manually
	// click through "ip addr show | grep inet" and "ps aux | grep ..." as
	// "critical" — alert-fatigue noise for commands that read nothing a local
	// `ip addr`/`ps` call wouldn't already show unchained. Deliberately still
	// excludes `env`/`printenv` (can leak secrets into the model's context,
	// see this var's own doc comment on why env stays off this list) and any
	// network client (curl/wget/nc/...) — those keep their existing
	// case-by-case risk treatment unchanged.
	"ip": true, "ss": true, "ps": true, "who": true, "w": true, "uname": true,
	"hostname": true, "arp": true, "netstat": true, "ifconfig": true, "route": true,
	"id": true, "whoami": true, "uptime": true, "df": true, "free": true,
	"lsof": true, "getent": true, "nproc": true,
}

// isSafeLocalPipeline reports whether cmd is a chain of ONLY local,
// read-only-or-append-only, non-network commands from safeLocalBinaries —
// e.g. "cat a.txt b.txt | sort -u > out.txt" or "echo ---; ls -la". This is
// a narrow, deliberate widening of classifyRisk's own allowlist-not-
// blacklist philosophy: a chained shell command is not automatically less
// safe than an unchained one when every segment individually names a
// program known incapable of reaching the network or spawning arbitrary
// other programs. Redirection (">"/">>") is allowed within a segment — it
// opens a file, it doesn't execute anything — but command/process
// substitution and background execution are refused outright, because the
// shell evaluates those before any "safe" command in the pipeline ever
// runs, regardless of which binaries are named. Anything this function
// doesn't confidently recognize — including a single unrecognized binary,
// or an unquoted-vs-quoted ambiguity it can't resolve — falls through to
// classifyRisk's existing critical-by-default floor unchanged; this is a
// discount on the risk BADGE only, see classifyRisk's own doc comment for
// what it does and does not affect.
func isSafeLocalPipeline(cmd string) bool {
	if strings.Contains(cmd, "$(") || strings.Contains(cmd, "`") ||
		strings.Contains(cmd, "<(") || strings.Contains(cmd, ">(") {
		return false
	}
	segments, bareAmpersand := splitUnquotedPipeline(cmd)
	if bareAmpersand || len(segments) < 2 {
		// A single, unchained segment is already classifyRisk's job via
		// simpleCommandPattern — this floor exists specifically for chains.
		// A bare "&" (background execution) is refused outright.
		return false
	}
	for _, seg := range segments {
		fields := strings.Fields(seg)
		if len(fields) == 0 || !safeLocalBinaries[fields[0]] {
			return false
		}
	}
	return true
}

// splitUnquotedPipeline splits cmd into segments at &&, ||, ;, and | — but
// only when those characters appear outside a '...'/"..." span, mirroring
// hasUnquotedShellMetachar's own quote tracking (validator_fuzz_test.go) so
// a literal operator character inside a quoted argument (e.g. grep "a|b")
// is never mistaken for a real chain boundary — a bug the fuzzer caught in
// an earlier, quote-blind version of this split. Text from an unquoted ">"
// to the next real separator (a redirection target) is dropped from its
// segment rather than treated as a new pipeline stage — the target is a
// file path, not a command, but any operator characters INSIDE it must
// still respect quoting, which is why this drop happens in the same
// quote-tracking pass rather than as a separate naive string operation.
// Also reports whether a standalone "&" (background execution) was found
// outside quotes.
func splitUnquotedPipeline(cmd string) (segments []string, bareAmpersand bool) {
	var current strings.Builder
	inSingle, inDouble, inRedirectTarget := false, false, false
	runes := []rune(cmd)
	flush := func() {
		segments = append(segments, strings.TrimSpace(current.String()))
		current.Reset()
		inRedirectTarget = false
	}
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case r == '\'' && !inDouble:
			inSingle = !inSingle
		case r == '"' && !inSingle:
			inDouble = !inDouble
		case inSingle || inDouble:
			if !inRedirectTarget {
				current.WriteRune(r)
			}
			continue
		case r == '&' && i+1 < len(runes) && runes[i+1] == '&':
			flush()
			i++
			continue
		case r == '|' && i+1 < len(runes) && runes[i+1] == '|':
			flush()
			i++
			continue
		case r == '&':
			bareAmpersand = true
		case r == ';' || r == '|':
			flush()
			continue
		case r == '>':
			inRedirectTarget = true
			continue
		}
		if !inRedirectTarget && r != '\'' && r != '"' {
			current.WriteRune(r)
		}
	}
	flush()
	return segments, bareAmpersand
}

// destructiveFloor is a second, independent risk floor for a gap
// classifyRisk deliberately does not cover: its allowlist only judges shell
// SHAPE safety (no chaining/redirection/substitution), not semantic danger —
// so "rm -rf /" and "dd of=/dev/sda" pass its simple-shape check exactly
// like "ls -la" does, since neither contains a shell metacharacter. This
// floor catches a small, deliberately narrow set of genuinely irreversible
// data/device-destroying command shapes and forces at least the given risk
// level regardless of the model's own risk_level or classifyRisk's verdict.
// This is NOT a general danger blacklist — everything else stays the
// model's judgment call, matching classifyRisk's own allowlist-not-blacklist
// philosophy; only clearly catastrophic, hard-to-undo actions are floored
// here. Returns ("", "") when nothing matches.
var (
	rmTok         = regexp.MustCompile(`(?i)(^|\s)rm(\s|$)`)
	recursiveFlag = regexp.MustCompile(`(?i)(^|\s)(-[a-z]*r[a-z]*|--recursive)(\s|$)`)
	forceFlag     = regexp.MustCompile(`(?i)(^|\s)(-[a-z]*f[a-z]*|--force)(\s|$)`)
	ddToDevice    = regexp.MustCompile(`(?i)(^|\s)dd(\s|$).*\bof=/dev/`)
	mkfsToDevice  = regexp.MustCompile(`(?i)(^|\s)mkfs(\.\w+)?(\s|$).*/dev/`)
	shredTok      = regexp.MustCompile(`(?i)(^|\s)shred(\s|$)`)
)

func destructiveFloor(cmd string) (risk, label string) {
	switch {
	case rmTok.MatchString(cmd) && recursiveFlag.MatchString(cmd) && forceFlag.MatchString(cmd):
		return "high", "recursive force delete (rm -rf)"
	case ddToDevice.MatchString(cmd):
		return "critical", "raw write to a block device (dd of=/dev/...)"
	case mkfsToDevice.MatchString(cmd):
		return "critical", "filesystem creation on a device (mkfs)"
	case shredTok.MatchString(cmd):
		return "high", "secure erase (shred)"
	default:
		return "", ""
	}
}

func requiresApproval(risk string) bool {
	switch strings.ToLower(risk) {
	case "medium", "high", "critical":
		return true
	default:
		return false
	}
}

// IsHighSeverity reports whether the given risk label is high or critical.
// Exported: agent/node_tool.go and agent/eino's runToolPipeline both call
// this to decide whether a task's per-task AutoApproveNonCritical toggle may
// skip the approval gate for a given proposal — "high"/"critical" always
// gate regardless of the toggle, matching this function's own definition of
// severe exactly rather than a second, potentially-diverging copy of it.
func IsHighSeverity(risk string) bool {
	switch strings.ToLower(strings.TrimSpace(risk)) {
	case "high", "critical":
		return true
	default:
		return false
	}
}

func maxRisk(a, b string) string {
	order := map[string]int{"low": 0, "medium": 1, "high": 2, "critical": 3}
	a = strings.ToLower(strings.TrimSpace(a))
	b = strings.ToLower(strings.TrimSpace(b))
	if order[b] > order[a] {
		return b
	}
	if _, ok := order[a]; ok {
		return a
	}
	return b
}

func uniqStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
