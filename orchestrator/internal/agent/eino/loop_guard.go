package eino

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/n0connect/yummi/orchestrator/internal/scope"
)

// loop_guard.go: the Eino backend's own copy of graph.go's stuck/duplicate/
// thrash detection (orchestrator/internal/agent's unexported commandHash/
// pushWindow/isStuck/windowCount/toolThrash, guarding node_tool.go). The
// graph backend has always had this; Eino never did — runToolPipeline's own
// comment claimed commands pass "validation, stuck, and duplicate checks"
// but no such check existed here, so a weak/distracted model (confirmed
// live, via OpenRouter) could re-run the exact same command indefinitely
// with no pushback, burning the step budget without making progress.
//
// A fresh, small, package-local copy rather than exporting and reusing the
// graph package's helpers: the logic is ~30 lines of pure functions with no
// dependency on GraphState, and duplicating it here keeps this fix isolated
// to the Eino backend — zero risk of a rename/refactor regressing the
// already-stable, more heavily relied-on graph backend.

const (
	loopWindowSize   = 6
	loopRepeatLimit  = 3
	loopToolThrashAt = 4
)

// loopGuard tracks one task's recent commands to catch repetition. One
// instance is created per Run() call and shared (via a pointer field on
// runCommandDeps/pentestToolDeps) across every tool call in that task — safe
// without a mutex because runner.go pins Eino's ToolsNode to
// ExecuteSequentially: true specifically so per-task state like this is
// never touched concurrently.
type loopGuard struct {
	hashes   []string // commandHash fingerprints, most recent last
	cmds     []string // raw commands, same order, for thrash/display
	families []string // hypothesis-family keys (host + route), most recent last
}

// check records command and returns a non-empty block reason if the task
// looks stuck: recent commands can't be re-run, since re-running the same
// shell/MCP call exactly reproduces its prior output — this exists to push
// the model toward a different command, update_plan, or finish instead.
func (g *loopGuard) check(command string) string {
	hash := loopCommandHash(command)
	g.hashes = loopPushWindow(g.hashes, hash, loopWindowSize)
	g.cmds = loopPushWindow(g.cmds, command, loopWindowSize)

	if loopIsStuck(g.hashes) {
		msg := "You are repeating the same commands. Recent: [" + strings.Join(g.cmds, ", ") +
			"]. STOP repeating — choose a DIFFERENT command, update the plan, or finish if the goal is already met."
		// Reset so a block doesn't re-trigger every single turn afterward —
		// matches node_tool.go's own reset-to-just-the-current-command.
		g.hashes = []string{hash}
		g.cmds = []string{command}
		return msg
	}
	if loopWindowCount(g.hashes, hash) >= 2 {
		return "You already ran this exact command recently: `" + command + "`\n" +
			"Re-running it produces the same output you already have. Choose a DIFFERENT command, " +
			"update the plan, or finish if the goal is met."
	}
	if tool, thrashing := loopToolThrash(g.cmds); thrashing {
		g.hashes = nil
		g.cmds = nil
		return "You have run `" + tool + "` several times with only trivially-different flags. These produce the SAME class of " +
			"information you already have — you are not learning anything new. STOP re-querying with `" + tool +
			"`: analyze the output you already collected, switch to a DIFFERENT tool/approach, update the plan, or finish if the goal is met."
	}
	// Hypothesis-level repetition: different tools and encodings can still be
	// one idea retried (e.g. printf/curl/for-loops all probing /cgi-bin/ on the
	// same host). Counted per host+route; only URL-form commands are counted.
	if fam := loopFamily(command); fam != "" {
		g.families = loopPushWindow(g.families, fam, loopWindowSize)
		if loopWindowCount(g.families, fam) >= loopFamilyLimit {
			g.families = nil
			return "You have made " + strconv.Itoa(loopFamilyLimit) + " attempts at the same route (" + fam + ") " +
				"without changing the underlying assumption. Name the assumption that the failed attempts " +
				"share, and test a DIFFERENT one (another route, another parameter, another technique), or " +
				"update the plan / finish."
		}
	}
	return ""
}

// loopFamilyLimit is how many URL-route attempts against one host+route are
// allowed in the window before the guard forces a change of hypothesis.
const loopFamilyLimit = 5

var loopHTTPMethod = regexp.MustCompile(`(get|post|put|head) /`)

// loopFamily returns a host+first-route-segment key for URL-form commands, or
// "" if the command has no URL path (nmap-style host scans are not counted).
// Encoded traversal sequences are decoded first so `.%2e/`, `%%2e`, and `..`
// variants of the same route map to the same key.
func loopFamily(cmd string) string {
	host := scope.ExtractTarget(cmd)
	if host == "" {
		return ""
	}
	low := strings.ToLower(cmd)
	low = strings.ReplaceAll(low, "%%", "%")
	low = strings.ReplaceAll(low, "%2e", ".")
	// Raw HTTP (printf/nc) carries the route after the method, not after the host.
	var rest string
	if m := loopHTTPMethod.FindStringIndex(low); m != nil {
		rest = low[m[1]-1:] // keep the leading "/"
	} else if idx := strings.Index(low, host); idx >= 0 {
		rest = low[idx+len(host):]
	}
	if !strings.HasPrefix(rest, "/") {
		return ""
	}
	seg := strings.TrimLeft(rest, "/")
	if end := strings.IndexAny(seg, "/ \"'\r\n\t"); end >= 0 {
		seg = seg[:end]
	}
	// A traversal's first segment is "." or ".." — the real route is after it.
	if seg == "" || strings.Trim(seg, ".") == "" {
		rest2 := strings.TrimLeft(rest, "/.")
		seg = rest2
		if end := strings.IndexAny(seg, "/ \"'\r\n\t"); end >= 0 {
			seg = seg[:end]
		}
	}
	if seg == "" {
		return ""
	}
	return host + "|" + seg
}

func loopCommandHash(cmd string) string {
	normalized := strings.Join(strings.Fields(cmd), " ")
	sum := sha256.Sum256([]byte(normalized))
	return fmt.Sprintf("%x", sum[:4])
}

func loopPushWindow(window []string, value string, cap int) []string {
	window = append(window, value)
	if len(window) > cap {
		window = window[len(window)-cap:]
	}
	return window
}

func loopWindowCount(window []string, value string) int {
	n := 0
	for _, v := range window {
		if v == value {
			n++
		}
	}
	return n
}

// loopIsStuck checks for >= loopRepeatLimit exact repeats or an A->B->A->B cycle.
func loopIsStuck(window []string) bool {
	if len(window) < loopRepeatLimit {
		return false
	}
	counts := make(map[string]int, len(window))
	for _, h := range window {
		counts[h]++
		if counts[h] >= loopRepeatLimit {
			return true
		}
	}
	if len(window) >= 4 {
		last := window[len(window)-4:]
		if last[0] == last[2] && last[1] == last[3] && last[0] != last[1] {
			return true
		}
	}
	return false
}

// loopToolThrash reports the tool name a window is thrashing on: the same
// first token appearing >= loopToolThrashAt times (catches "ps -ef" / "ps
// -ejH" / "ps aux" rotation, which exact-hash matching misses).
func loopToolThrash(commands []string) (string, bool) {
	counts := make(map[string]int, len(commands))
	for _, c := range commands {
		fields := strings.Fields(c)
		if len(fields) == 0 {
			continue
		}
		tool := fields[0]
		counts[tool]++
		if counts[tool] >= loopToolThrashAt {
			return tool, true
		}
	}
	return "", false
}
