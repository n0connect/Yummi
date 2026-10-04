package main

// terminal.go turns this MCP server into a persistent, multi-terminal session
// manager for the agent — the "GUI-ending" surface the operator asked for.
//
// The one-shot capture.go path (runCaptured*) opens a process, buffers its
// output, and exits: it cannot connect a VPN, answer an interactive sqlmap
// prompt, hold a reverse-shell listener, or serve a file over a background
// http.server while a second terminal fetches it. Those are exactly the
// repetitive manual steps a human does in a Kali box.
//
// Backing store is tmux. tmux gives three things a raw PTY would force us to
// reimplement: (1) persistence — the tmux server is a separate daemon, so
// sessions survive this MCP process restarting; (2) scrollback — capture-pane
// reads history for free; (3) detach/reattach — the operator's own Bash tab
// can attach the very same session the agent is driving. Every tmux call goes
// through exec.Command("tmux", ...) with an argv slice — never a shell — so
// there is no metacharacter-injection surface at THIS layer. The command TEXT
// the agent sends is deliberately interpreted by the in-pane shell: that is
// the whole point (full shell power), and it is gated at the packet layer by
// the fail-closed gateway, not here.
//
// Security posture: these tools have no single "target field", so the
// orchestrator-side catalog marks them TargetField:"" (NeedsTarget()=false)
// and the per-command scope check is skipped — external reachability is still
// enforced by the gateway's nftables FORWARD chain (default-deny), so a
// session can only reach what an operator has explicitly allowed, regardless
// of what command text runs inside it.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	tmuxSessionPrefix = "yummi-"
	// defaultCols/Rows are deliberately wide so tool output wraps as little
	// as possible in the captured pane (a narrow pane mangles nmap/nikto
	// tables). 200x50 matches a comfortable operator terminal.
	defaultCols = 200
	defaultRows = 50
	// pollInterval is how often we re-capture the pane while waiting for a
	// completion sentinel or a wait pattern. 250ms is imperceptible for a
	// pentest tool and keeps CPU near zero.
	pollInterval = 250 * time.Millisecond
	// settleDelay is the grace period after first seeing the END sentinel
	// before the final, authoritative re-capture (see termRunHandler) —
	// closes a real render-ordering gap found live-testing against the
	// actual sandbox container, not a theoretical concern.
	settleDelay = 60 * time.Millisecond
	// defaultRunTimeout applies when the caller does not set timeout_secs on
	// a waiting term_run. Long scans should pass a larger value or use
	// wait=false + term_wait.
	defaultRunTimeout = 30 * time.Second
	// maxRunTimeout caps any single blocking wait so a stuck sentinel can
	// never wedge the MCP handler indefinitely.
	maxRunTimeout = 30 * time.Minute
	// defaultReadLines is the tail length term_read returns when unspecified.
	defaultReadLines = 200
)

// sessIDPattern constrains an agent-supplied session id/label to what tmux
// accepts as a session name (no '.' or ':') and to something safe to embed in
// an argv slot. We additionally forbid a leading '-' so the value can never be
// mistaken for a tmux flag.
var sessIDPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]{0,39}$`)

// terminalManager holds the small amount of state tmux itself does not track:
// a human label per session and a monotonic counter for auto-generated ids.
// tmux remains the source of truth for liveness; if this process restarts,
// term_list rebuilds the session set from tmux and simply loses the labels.
type terminalManager struct {
	mu      sync.Mutex
	labels  map[string]string // sessionID -> operator/agent label
	counter int
}

var terminals = &terminalManager{labels: map[string]string{}}

// tmuxMu serializes every tmux CLI invocation this process makes. tmux runs
// as a single server process shared by every session; live-testing this
// package against a real sandbox container under real concurrency (5
// sessions opened and driven in parallel, matching how an agent actually
// uses this feature) reproducibly showed dropped/misdirected command output
// — a command's own echo line would appear in one pane's scrollback with no
// result line following it, or a variable set in one session would appear to
// leak into another — when multiple goroutines issued send-keys/capture-pane
// against the shared tmux server truly simultaneously (as separate OS
// processes). This is a tmux-server contention race, not an MCP or Go-level
// bug: reproduced identically with plain concurrent shell invocations of the
// tmux CLI, no Go code involved at all.
//
// Each individual tmux CLI call is single-digit milliseconds, so serializing
// them adds no perceptible latency, and it does NOT serialize the actual
// business the agent cares about: the shells/tools running inside each pty
// keep running fully independently and concurrently — only the brief act of
// "type these keys" / "read this screen" is queued. Without this, "the agent
// opens and drives N terminals at once" — the entire point of this feature —
// was not reliable under real concurrent use.
var tmuxMu sync.Mutex

// sessionLocks serializes key-sending operations (term_run, term_keys)
// targeting the SAME session id, while leaving different sessions fully
// independent/parallel. tmuxMu alone only makes each individual tmux CLI
// invocation atomic; it does nothing to stop two separate term_run calls
// against the same session from being in flight at once, each typing its
// own multi-line sentinel sequence — stress-testing this package against
// the real sandbox with two goroutines racing on one session id
// reproducibly showed one call's command text getting echoed onto the same
// line as another call's own sentinel output. A session is a single shell;
// only one logical "type these keys" operation may be in progress against
// it at a time, matching how a human could only type into one terminal
// with one pair of hands. Acquired by name via lockSession/unlockSession;
// released (map entry removed) on term_close so it does not grow forever.
var sessionLocks sync.Map // map[string]*sync.Mutex

func lockSession(id string) {
	v, _ := sessionLocks.LoadOrStore(id, &sync.Mutex{})
	v.(*sync.Mutex).Lock()
}

func unlockSession(id string) {
	if v, ok := sessionLocks.Load(id); ok {
		v.(*sync.Mutex).Unlock()
	}
}

func nonceHex() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// tmuxName maps an agent-facing session id to the prefixed tmux session name.
func tmuxName(id string) string { return tmuxSessionPrefix + id }

// runTmux executes a tmux subcommand and returns its combined output. A
// non-zero exit is returned as an error carrying that output, since tmux
// writes its diagnostics ("can't find session", "no server running") to
// stderr and they are the useful part of the failure.
func runTmux(ctx context.Context, args ...string) (string, error) {
	tmuxMu.Lock()
	defer tmuxMu.Unlock()
	cmd := exec.CommandContext(ctx, "tmux", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("tmux %s: %s", strings.Join(args, " "), msg)
	}
	return string(out), nil
}

func tmuxAvailable() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

// capturePane returns the pane contents. When full is true the entire
// scrollback is returned (bounded afterwards by the caller); otherwise only
// the last `lines` visible/history lines.
func capturePane(ctx context.Context, id string, full bool, lines int) (string, error) {
	args := []string{"capture-pane", "-t", tmuxName(id), "-p"}
	if full {
		args = append(args, "-S", "-")
	} else {
		args = append(args, "-S", "-"+strconv.Itoa(lines))
	}
	return runTmux(ctx, args...)
}

// ---- tool argument/result structs ----

type termOpenArgs struct {
	Label string `json:"label,omitempty" jsonschema:"optional human-readable name/id for the session, e.g. 'target-shell' (letters, digits, _ and -). Auto-generated when omitted."`
	Cols  int    `json:"cols,omitempty" jsonschema:"optional terminal width in columns (default 200)"`
	Rows  int    `json:"rows,omitempty" jsonschema:"optional terminal height in rows (default 50)"`
}

type termRunArgs struct {
	ID          string `json:"id" jsonschema:"session id returned by term_open"`
	Command     string `json:"command" jsonschema:"shell command line to type into the session (interpreted by the in-pane shell; pipes, &&, redirection all work)"`
	Wait        *bool  `json:"wait,omitempty" jsonschema:"true (default): block until the command finishes and return its output+exit code. false: fire-and-return for interactive or long-running commands (VPN, listeners, servers) — then use term_wait/term_read."`
	TimeoutSecs int    `json:"timeout_secs,omitempty" jsonschema:"max seconds to wait when wait=true (default 30, max 1800)"`
}

type termReadArgs struct {
	ID    string `json:"id" jsonschema:"session id"`
	Lines int    `json:"lines,omitempty" jsonschema:"how many trailing lines to return (default 200)"`
	Full  bool   `json:"full,omitempty" jsonschema:"return the entire scrollback instead of just the tail"`
}

type termWaitArgs struct {
	ID          string `json:"id" jsonschema:"session id"`
	Pattern     string `json:"pattern,omitempty" jsonschema:"optional regular expression; return as soon as the pane tail matches it (e.g. 'password:' or 'yes/no')"`
	IdleSecs    int    `json:"idle_secs,omitempty" jsonschema:"optional: return once the pane has not changed for this many seconds (detects a command that finished/settled)"`
	TimeoutSecs int    `json:"timeout_secs,omitempty" jsonschema:"max seconds to wait (default 30, max 1800)"`
}

type termKeysArgs struct {
	ID      string `json:"id" jsonschema:"session id"`
	Keys    string `json:"keys" jsonschema:"keys or command to send. Supports tmux key names (e.g. 'C-c', 'Enter', 'Up Up Enter') as well as text and commands. Spaces, quotes, and punctuation are fully preserved."`
	Literal bool   `json:"literal,omitempty" jsonschema:"force sending keys strictly as literal characters without interpreting any tmux key names"`
	Enter   bool   `json:"enter,omitempty" jsonschema:"append a Return keypress after sending (useful when typing commands or answering prompts)"`
}

type termIDArg struct {
	ID string `json:"id" jsonschema:"session id"`
}

// ---- handlers ----

func termOpenHandler(ctx context.Context, _ *mcp.CallToolRequest, args termOpenArgs) (*mcp.CallToolResult, any, error) {
	if !tmuxAvailable() {
		return errResult("tmux is not installed in this sandbox — the terminal-session tools require it")
	}
	id := strings.TrimSpace(args.Label)
	terminals.mu.Lock()
	if id == "" {
		terminals.counter++
		id = fmt.Sprintf("t%d-%s", terminals.counter, nonceHex()[:4])
	}
	terminals.mu.Unlock()

	if !sessIDPattern.MatchString(id) {
		return errResult(fmt.Sprintf("invalid session id %q — use letters, digits, _ and - (max 40 chars, no leading -)", id))
	}
	// term_open is deliberately IDEMPOTENT on an already-live id: if the
	// session is already open, report success and reuse it rather than
	// erroring. Two things made this the right behavior, not just a
	// convenience: (1) an id embeds a nonce/timestamp precisely so a caller
	// only ever reuses one on purpose (e.g. "ensure a terminal named
	// 'target' exists"), so treating that as success does what the caller
	// almost certainly wants; (2) on the MCP transport used to reach this
	// server (a persistent docker-exec-i pipe shared by every concurrent
	// tool call — Eino's ToolsNode genuinely executes several tool calls in
	// parallel within one model turn), a transient transport hiccup can make
	// the CLIENT believe a call failed and transparently retry it, even
	// though the original request already succeeded server-side. Before
	// this, that retry hit this exact check and failed hard with "already
	// exists" — a spurious, non-recoverable error for what was actually a
	// successful operation. Erroring here is still correct for the ONE case
	// that matters: an id whose tmux session is alive but was opened by a
	// genuinely different, unrelated call — but there is no way to
	// distinguish that from "my own retry" at this layer, and unlike, say,
	// two callers racing to run a destructive command, reusing an
	// already-open terminal has no harmful effect either way.
	if _, err := runTmux(ctx, "has-session", "-t", tmuxName(id)); err == nil {
		return textResult(fmt.Sprintf("session %q already exists — reusing it. Use term_run with id=%q to run commands.", id, id))
	}

	cols, rows := args.Cols, args.Rows
	if cols <= 0 {
		cols = defaultCols
	}
	if rows <= 0 {
		rows = defaultRows
	}
	if _, err := runTmux(ctx, "new-session", "-d", "-s", tmuxName(id),
		"-x", strconv.Itoa(cols), "-y", strconv.Itoa(rows)); err != nil {
		return errResult(err.Error())
	}
	// Generous scrollback so long scans are not lost from history.
	_, _ = runTmux(ctx, "set-option", "-t", tmuxName(id), "history-limit", "100000")

	terminals.mu.Lock()
	terminals.labels[id] = args.Label
	terminals.mu.Unlock()

	return textResult(fmt.Sprintf("opened terminal session %q (%dx%d). Use term_run with id=%q to run commands.", id, cols, rows, id))
}

func termRunHandler(ctx context.Context, _ *mcp.CallToolRequest, args termRunArgs) (*mcp.CallToolResult, any, error) {
	id := strings.TrimSpace(args.ID)
	if err := requireSession(ctx, id); err != nil {
		return errResult(err.Error())
	}
	if strings.TrimSpace(args.Command) == "" {
		return errResult("command is required")
	}
	// Serialize against any other term_run/term_keys targeting this SAME
	// session — see sessionLocks' doc comment. Different sessions are
	// unaffected (each gets its own lock).
	lockSession(id)
	defer unlockSession(id)
	wait := args.Wait == nil || *args.Wait

	if !wait {
		// Fire-and-return: type the command, press Enter, do not wait.
		if err := sendLine(ctx, id, args.Command); err != nil {
			return errResult(err.Error())
		}
		return textResult(fmt.Sprintf("[%s] sent (no wait). Use term_wait/term_read to observe.", id))
	}

	nonce := nonceHex()
	begMark := "__YUMMI_BEG_" + nonce + "__"
	endPrefix := "__YUMMI_END_" + nonce + "__"
	endRe := regexp.MustCompile(regexp.QuoteMeta(endPrefix) + `(-?\d+)__`)

	// Emit BEG marker, the real command, then an END marker carrying $?, as
	// ONE combined literal write (see sendLines) — each is still its own
	// shell line (real \n between them), so $? in the END printf still
	// reflects the command's exit status; nothing else runs between them.
	if err := sendLines(ctx, id,
		"printf '"+begMark+"\\n'",
		args.Command,
		"printf '\\n"+endPrefix+"%d__\\n' \"$?\"",
	); err != nil {
		return errResult(err.Error())
	}

	timeout := resolveTimeout(args.TimeoutSecs, defaultRunTimeout)
	deadline := time.Now().Add(timeout)
	for {
		pane, err := capturePane(ctx, id, true, 0)
		if err != nil {
			return errResult(err.Error())
		}
		if m := endRe.FindStringSubmatch(pane); m != nil {
			// Settle-and-recapture: the END sentinel appearing in this
			// snapshot only proves bash has already run the trailing printf,
			// not that tmux's own pane/grid has finished applying every byte
			// the command itself wrote a moment earlier. Live-testing against
			// the real sandbox reproducibly showed the command's own output
			// line occasionally missing from THIS EXACT capture even though
			// the END marker (written after it, by the same shell, on the
			// very next line) was already present — i.e. a real ordering gap
			// between "bash has run the next command" and "tmux has fully
			// rendered the previous command's output into the buffer
			// capture-pane reads from". A short grace period plus one more
			// capture closes that gap without needing to understand tmux's
			// internal buffering in detail. Re-checks the sentinel is still
			// there (it always will be — nothing types over it) purely so a
			// context cancellation during the sleep is handled cleanly.
			select {
			case <-ctx.Done():
				return errResult("context cancelled while waiting for command")
			case <-time.After(settleDelay):
			}
			settled, err := capturePane(ctx, id, true, 0)
			if err != nil {
				return errResult(err.Error())
			}
			if m2 := endRe.FindStringSubmatch(settled); m2 != nil {
				pane, m = settled, m2
			} // else: extremely unlikely (sentinel can't un-render); fall back to the first capture.
			code, _ := strconv.Atoi(m[1])
			out := extractBetween(pane, begMark, endPrefix, nonce)
			status := "completed"
			return textResult(fmt.Sprintf("[%s] %s (exit=%d)\n%s", id, status, code, out))
		}
		if time.Now().After(deadline) {
			out := tailAfter(pane, begMark, nonce)
			return textResult(fmt.Sprintf("[%s] still running after %s (no exit yet). Partial output:\n%s\nUse term_wait/term_keys to interact or term_read to poll.", id, timeout, out))
		}
		select {
		case <-ctx.Done():
			return errResult("context cancelled while waiting for command")
		case <-time.After(pollInterval):
		}
	}
}

func termReadHandler(ctx context.Context, _ *mcp.CallToolRequest, args termReadArgs) (*mcp.CallToolResult, any, error) {
	id := strings.TrimSpace(args.ID)
	if err := requireSession(ctx, id); err != nil {
		return errResult(err.Error())
	}
	lines := args.Lines
	if lines <= 0 {
		lines = defaultReadLines
	}
	pane, err := capturePane(ctx, id, args.Full, lines)
	if err != nil {
		return errResult(err.Error())
	}
	return textResult(capString(strings.TrimRight(pane, "\n")))
}

func termWaitHandler(ctx context.Context, _ *mcp.CallToolRequest, args termWaitArgs) (*mcp.CallToolResult, any, error) {
	id := strings.TrimSpace(args.ID)
	if err := requireSession(ctx, id); err != nil {
		return errResult(err.Error())
	}
	var pat *regexp.Regexp
	if strings.TrimSpace(args.Pattern) != "" {
		p, err := regexp.Compile(args.Pattern)
		if err != nil {
			return errResult(fmt.Sprintf("invalid pattern regexp: %v", err))
		}
		pat = p
	}
	if pat == nil && args.IdleSecs <= 0 {
		return errResult("term_wait needs at least one of: pattern, idle_secs")
	}

	timeout := resolveTimeout(args.TimeoutSecs, defaultRunTimeout)
	deadline := time.Now().Add(timeout)
	idleWindow := time.Duration(args.IdleSecs) * time.Second

	var lastPane string
	var lastChange time.Time
	for {
		pane, err := capturePane(ctx, id, false, defaultReadLines)
		if err != nil {
			return errResult(err.Error())
		}
		if pat != nil && pat.MatchString(pane) {
			return textResult(fmt.Sprintf("[%s] matched pattern.\n%s", id, capString(strings.TrimRight(pane, "\n"))))
		}
		now := time.Now()
		if pane != lastPane {
			lastPane = pane
			lastChange = now
		} else if idleWindow > 0 && !lastChange.IsZero() && now.Sub(lastChange) >= idleWindow {
			return textResult(fmt.Sprintf("[%s] idle for %s.\n%s", id, idleWindow, capString(strings.TrimRight(pane, "\n"))))
		}
		if now.After(deadline) {
			return textResult(fmt.Sprintf("[%s] wait timed out after %s (no match / not idle).\n%s", id, timeout, capString(strings.TrimRight(pane, "\n"))))
		}
		select {
		case <-ctx.Done():
			return errResult("context cancelled while waiting")
		case <-time.After(pollInterval):
		}
	}
}

var tmuxNamedKeys = map[string]bool{
	"enter": true, "return": true, "space": true, "tab": true, "btab": true,
	"bspace": true, "backspace": true, "escape": true, "esc": true,
	"up": true, "down": true, "left": true, "right": true,
	"home": true, "end": true, "pageup": true, "pagedown": true,
	"pgup": true, "pgdn": true, "npage": true, "ppage": true,
	"insert": true, "delete": true, "ic": true, "dc": true,
	"f1": true, "f2": true, "f3": true, "f4": true, "f5": true, "f6": true,
	"f7": true, "f8": true, "f9": true, "f10": true, "f11": true, "f12": true,
	"f13": true, "f14": true, "f15": true, "f16": true, "f17": true, "f18": true,
	"f19": true, "f20": true, "f21": true, "f22": true, "f23": true, "f24": true,
	"kpenter": true, "kp/": true, "kp*": true, "kp-": true, "kp+": true, "kp.": true,
	"kp0": true, "kp1": true, "kp2": true, "kp3": true, "kp4": true, "kp5": true,
	"kp6": true, "kp7": true, "kp8": true, "kp9": true,
}

// isTmuxKey reports whether a token is a recognized tmux key name or key chord.
func isTmuxKey(token string) bool {
	if token == "" {
		return false
	}
	lower := strings.ToLower(token)
	if tmuxNamedKeys[lower] {
		return true
	}
	for _, prefix := range []string{"c-", "ctrl-", "m-", "alt-", "s-", "shift-"} {
		if strings.HasPrefix(lower, prefix) {
			suffix := lower[len(prefix):]
			if suffix == "" {
				return false
			}
			if len(suffix) == 1 {
				return true
			}
			if tmuxNamedKeys[suffix] {
				return true
			}
		}
	}
	return false
}

// isAllTmuxKeys reports whether every whitespace-separated token in s is a recognized tmux key.
func isAllTmuxKeys(s string) bool {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return false
	}
	for _, f := range fields {
		if !isTmuxKey(f) {
			return false
		}
	}
	return true
}

type keySegment struct {
	text  string
	isKey bool
}

func parseKeySegments(s string) []keySegment {
	fields := strings.Fields(s)
	hasAnyKey := false
	for _, f := range fields {
		if isTmuxKey(f) {
			hasAnyKey = true
			break
		}
	}
	if !hasAnyKey {
		return []keySegment{{text: s, isKey: false}}
	}

	var segments []keySegment
	var currentText strings.Builder

	i := 0
	for i < len(s) {
		if s[i] == ' ' || s[i] == '	' {
			currentText.WriteByte(s[i])
			i++
			continue
		}
		start := i
		for i < len(s) && s[i] != ' ' && s[i] != '	' {
			i++
		}
		token := s[start:i]

		if isTmuxKey(token) {
			if currentText.Len() > 0 {
				trimmed := strings.TrimRight(currentText.String(), " 	")
				if trimmed != "" {
					segments = append(segments, keySegment{text: trimmed, isKey: false})
				}
				currentText.Reset()
			}
			segments = append(segments, keySegment{text: token, isKey: true})
		} else {
			currentText.WriteString(token)
		}
	}
	if currentText.Len() > 0 {
		segments = append(segments, keySegment{text: currentText.String(), isKey: false})
	}
	return segments
}

func termKeysHandler(ctx context.Context, _ *mcp.CallToolRequest, args termKeysArgs) (*mcp.CallToolResult, any, error) {
	id := strings.TrimSpace(args.ID)
	if err := requireSession(ctx, id); err != nil {
		return errResult(err.Error())
	}
	if args.Keys == "" && !args.Enter {
		return errResult("keys is required")
	}
	// Same per-session serialization as termRunHandler. In the intended
	// usage pattern (wait=false term_run, then term_wait/term_keys), this
	// never contends: a wait=false term_run only holds the lock for the
	// brief moment it takes to type the command, not across the command's
	// whole runtime.
	lockSession(id)
	defer unlockSession(id)
	base := []string{"send-keys", "-t", tmuxName(id)}

	if args.Literal {
		if args.Keys != "" {
			if _, err := runTmux(ctx, append(append([]string{}, base...), "-l", "--", args.Keys)...); err != nil {
				return errResult(err.Error())
			}
		}
		if args.Enter {
			if _, err := runTmux(ctx, append(append([]string{}, base...), "--", "Enter")...); err != nil {
				return errResult(err.Error())
			}
		}
		return textResult(fmt.Sprintf("[%s] keys sent.", id))
	}

	if args.Keys != "" {
		if isAllTmuxKeys(args.Keys) {
			// Pure space-separated tmux key names (e.g. "C-c", "Up Up Enter", "C-c C-c").
			if _, err := runTmux(ctx, append(append(append([]string{}, base...), "--"), strings.Fields(args.Keys)...)...); err != nil {
				return errResult(err.Error())
			}
		} else if args.Enter {
			// When Enter is requested alongside non-key text, the caller is typing a command or line
			// of input (e.g. "find / -name user.txt 2>/dev/null", "ls -la /home", "python3 -c '...'").
			// Sending literally (-l --) preserves all spaces, quotes, and punctuation without tmux
			// command parsing errors on ';'.
			if _, err := runTmux(ctx, append(append([]string{}, base...), "-l", "--", args.Keys)...); err != nil {
				return errResult(err.Error())
			}
		} else {
			// Enter is false: parse into segments of literal text and special tmux keys (e.g. "y Enter", "admin Tab pass Enter").
			segments := parseKeySegments(args.Keys)
			for _, seg := range segments {
				if seg.isKey {
					if _, err := runTmux(ctx, append(append([]string{}, base...), "--", seg.text)...); err != nil {
						return errResult(err.Error())
					}
				} else {
					if _, err := runTmux(ctx, append(append([]string{}, base...), "-l", "--", seg.text)...); err != nil {
						return errResult(err.Error())
					}
				}
			}
		}
	}

	if args.Enter {
		if _, err := runTmux(ctx, append(append([]string{}, base...), "--", "Enter")...); err != nil {
			return errResult(err.Error())
		}
	}
	return textResult(fmt.Sprintf("[%s] keys sent.", id))
}

func termListHandler(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	if !tmuxAvailable() {
		return errResult("tmux is not installed in this sandbox")
	}
	out, err := runTmux(ctx, "list-sessions", "-F", "#{session_name}\t#{session_windows}w\t#{window_width}x#{window_height}\t#{t:session_created}")
	if err != nil {
		// tmux exits non-zero with "no server running" when nothing is open.
		if strings.Contains(err.Error(), "no server running") || strings.Contains(err.Error(), "error connecting") {
			return textResult("no open sessions")
		}
		return errResult(err.Error())
	}
	var b strings.Builder
	terminals.mu.Lock()
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if !strings.HasPrefix(line, tmuxSessionPrefix) {
			continue // ignore any non-Yummi tmux session (e.g. operator's own)
		}
		fields := strings.SplitN(line, "\t", 4)
		id := strings.TrimPrefix(fields[0], tmuxSessionPrefix)
		label := terminals.labels[id]
		fields[0] = id
		if label != "" && label != id {
			fields = append(fields, "label="+label)
		}
		b.WriteString(strings.Join(fields, "  "))
		b.WriteByte('\n')
	}
	terminals.mu.Unlock()
	if b.Len() == 0 {
		return textResult("no open sessions")
	}
	return textResult(strings.TrimRight(b.String(), "\n"))
}

func termCloseHandler(ctx context.Context, _ *mcp.CallToolRequest, args termIDArg) (*mcp.CallToolResult, any, error) {
	id := strings.TrimSpace(args.ID)
	if !sessIDPattern.MatchString(id) {
		return errResult(fmt.Sprintf("invalid session id %q", id))
	}
	if _, err := runTmux(ctx, "kill-session", "-t", tmuxName(id)); err != nil {
		return errResult(err.Error())
	}
	terminals.mu.Lock()
	delete(terminals.labels, id)
	terminals.mu.Unlock()
	sessionLocks.Delete(id) // no longer a live session; drop its lock entry
	return textResult(fmt.Sprintf("closed session %q", id))
}

// ---- helpers ----

// requireSession validates the id shape and confirms the session is live.
func requireSession(ctx context.Context, id string) error {
	if !tmuxAvailable() {
		return fmt.Errorf("tmux is not installed in this sandbox")
	}
	if !sessIDPattern.MatchString(id) {
		return fmt.Errorf("invalid session id %q — use letters, digits, _ and - (max 40 chars, no leading -)", id)
	}
	if _, err := runTmux(ctx, "has-session", "-t", tmuxName(id)); err != nil {
		return fmt.Errorf("no such session %q (open one with term_open first)", id)
	}
	return nil
}

// sendLine types a full command line into the pane and presses Enter. The
// text is sent literally (-l --) so no character is interpreted as a tmux key
// binding; the in-pane shell is what interprets it.
func sendLine(ctx context.Context, id, line string) error {
	if _, err := runTmux(ctx, "send-keys", "-t", tmuxName(id), "-l", "--", line); err != nil {
		return err
	}
	_, err := runTmux(ctx, "send-keys", "-t", tmuxName(id), "--", "Enter")
	return err
}

// sendLines types several complete lines into the pane as ONE literal
// send-keys invocation, with a real newline byte embedded between (and
// after) each line, instead of one send-keys-plus-Enter pair per line.
//
// This matters specifically for termRunHandler's three-line BEG/command/END
// sentinel sequence. Sending each line as a SEPARATE tmux send-keys call
// (text, then a separate Enter, repeated) opens a real race against bash's
// own interactive line editor: readline does not necessarily finish
// "accepting" one Enter (parsing the line, running it, printing output, and
// redrawing a fresh prompt) before the NEXT line's raw bytes arrive at the
// pty, especially under real load (tmux server handling several sessions,
// virtualized/containerized CPU scheduling jitter). When that happens,
// readline treats the new bytes as still-being-typed input for the CURRENT
// line rather than a new command — live-testing against the real sandbox
// reproducibly showed a later line's text getting echoed onto the same
// screen row as an earlier command's own sentinel output, and the earlier
// command's real output line going missing from the captured pane entirely.
//
// Sending everything as ONE literal write (mirroring how pasting several
// lines of text into an interactive terminal works, which is reliable
// precisely because the shell receives the whole paste as one contiguous
// byte stream) avoids the gap: there is no window between lines for another
// event to land in.
func sendLines(ctx context.Context, id string, lines ...string) error {
	payload := strings.Join(lines, "\n") + "\n"
	_, err := runTmux(ctx, "send-keys", "-t", tmuxName(id), "-l", "--", payload)
	return err
}

func resolveTimeout(secs int, def time.Duration) time.Duration {
	if secs <= 0 {
		return def
	}
	t := time.Duration(secs) * time.Second
	if t > maxRunTimeout {
		return maxRunTimeout
	}
	return t
}

// extractBetween returns the pane lines between the BEG marker line and the
// END marker line for a run, dropping every line that carries the run's
// nonce (the marker outputs and their own typed-command echoes). Remaining
// lines — the real command echo (with shell prompt) and its output — are the
// "screen" the agent sees, mirroring what a human would read. Bounded by
// capString.
func extractBetween(pane, begMark, endPrefix, nonce string) string {
	lines := strings.Split(pane, "\n")
	beg, end := -1, -1
	for i := len(lines) - 1; i >= 0; i-- { // scan from bottom: use the LAST run
		if end == -1 && strings.Contains(lines[i], endPrefix) {
			end = i
			continue
		}
		if end != -1 && strings.Contains(lines[i], begMark) {
			beg = i
			break
		}
	}
	if beg == -1 || end == -1 || end <= beg {
		return capString(tailAfter(pane, begMark, nonce))
	}
	var kept []string
	for _, l := range lines[beg+1 : end] {
		if strings.Contains(l, nonce) {
			continue
		}
		kept = append(kept, l)
	}
	return capString(strings.TrimRight(strings.Join(kept, "\n"), "\n"))
}

// tailAfter returns pane content from the BEG marker onward (nonce lines
// stripped), used when a run has not finished yet so we can still show the
// operator/agent the partial output.
func tailAfter(pane, begMark, nonce string) string {
	lines := strings.Split(pane, "\n")
	beg := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], begMark) {
			beg = i
			break
		}
	}
	if beg == -1 {
		return capString(strings.TrimRight(pane, "\n"))
	}
	var kept []string
	for _, l := range lines[beg+1:] {
		if strings.Contains(l, nonce) {
			continue
		}
		kept = append(kept, l)
	}
	return strings.TrimRight(strings.Join(kept, "\n"), "\n")
}

// capString bounds returned text to the same 1 MiB ceiling capture.go uses,
// keeping the TAIL (most recent output is what matters when truncating a live
// terminal screen).
func capString(s string) string {
	if len(s) <= maxOutputBytes {
		return s
	}
	return truncationMark + "\n" + s[len(s)-maxOutputBytes:]
}

// textResult wraps a successful text payload, mirroring runCaptured's success
// shape.
func textResult(text string) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil, nil
}

// registerTerminalTools wires the seven session verbs into the MCP server.
func registerTerminalTools(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "term_open",
		Description: "Open a new persistent terminal (tmux) session and return its id. Sessions survive across tool calls; open as many as you need (e.g. one to drive the target, one to serve a payload).",
	}, termOpenHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "term_run",
		Description: "Type a command into a session. wait=true (default) blocks for the result and returns exit code+output; wait=false fires and returns immediately for interactive/long-running commands (VPN, listeners, http.server).",
	}, termRunHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "term_read",
		Description: "Read a session's current screen/scrollback (the tail by default). Use to inspect a fire-and-return command's progress.",
	}, termReadHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "term_wait",
		Description: "Block until a session's output matches a pattern (e.g. 'password:', 'yes/no') or has been idle for idle_secs. This is how you 'watch' an interactive/long command instead of a human staring at the screen.",
	}, termWaitHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "term_keys",
		Description: "Send raw keys to a session — tmux key names by default (C-c, Enter, 'y Enter', 'Up Up Enter') or literal characters. Use to answer interactive prompts or interrupt a running command.",
	}, termKeysHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "term_list",
		Description: "List open terminal sessions with size and label.",
	}, termListHandler)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "term_close",
		Description: "Close (kill) a terminal session.",
	}, termCloseHandler)
}
