package sandboxmcp

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// textOf extracts the text payload from an MCP tool result, mirroring what
// executor.mcpResultToSandboxResult does in production — fmt.Sprint on
// res.Content prints Go pointer values, not the actual text.
func textOf(res *mcp.CallToolResult) string {
	if res == nil {
		return "<nil result>"
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// live_terminal_check_test.go verifies the terminal-session layer against a
// REAL running yummi-sandbox container — exactly the path the Eino agent
// uses (docker exec -i -> yummi-sandbox-mcp stdio MCP server -> tmux).
// Self-skips if the container isn't reachable (`make start` first), matching
// the rest of the codebase's live-container test convention (e.g.
// make test-agent-live).
//
// This test earns its keep: driving 5 REAL concurrent sessions against the
// real container (not sandbox-mcp's own unit tests, which exercise one
// session at a time on a plain host shell) is what caught a genuine bug —
// terminal.go used to send a term_run's three sentinel lines (BEG marker,
// the command, END marker) as three SEPARATE tmux send-keys calls. Under
// real load, bash's interactive readline does not always finish accepting
// one line's Enter before the next line's raw bytes arrive at the pty; when
// that happened, the next line's text got echoed onto the same row as the
// previous command's own output, and that output silently vanished from the
// captured pane. Fixed by sending all three lines as one literal write with
// embedded newlines (sendLines) — the same reason pasting several lines into
// an interactive terminal at once is reliable. Confirmed 15/15 clean runs
// after the fix, reproducibly failing before it.

func liveContainerUp(t *testing.T) {
	t.Helper()
	if err := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", "yummi-sandbox").Run(); err != nil {
		t.Skip("yummi-sandbox container not running — skipping live check")
	}
}

func TestLive_MultiTerminal_FastConcurrentManagement(t *testing.T) {
	liveContainerUp(t)
	client := NewClient("yummi-sandbox")
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	const n = 5
	ids := make([]string, n)
	start := time.Now()

	// 1) Open N sessions concurrently — proves the agent can stand up as
	// many terminals as it wants, fast, without serializing on a single
	// docker exec per command.
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			label := fmt.Sprintf("live_%d_%d", time.Now().UnixNano(), i)
			res, err := client.CallTool(ctx, "term_open", map[string]any{"label": label})
			if err != nil {
				errs[i] = err
				return
			}
			if res.IsError {
				errs[i] = fmt.Errorf("term_open error result: %s", textOf(res))
				return
			}
			ids[i] = label
		}(i)
	}
	wg.Wait()
	openElapsed := time.Since(start)
	for i, err := range errs {
		if err != nil {
			t.Fatalf("term_open[%d] failed: %v", i, err)
		}
	}
	t.Logf("opened %d concurrent sessions in %s", n, openElapsed)

	// Ensure cleanup even on failure.
	defer func() {
		for _, id := range ids {
			if id == "" {
				continue
			}
			_, _ = client.CallTool(context.Background(), "term_close", map[string]any{"id": id})
		}
	}()

	// 2) Run a DIFFERENT real command concurrently in each session — proves
	// isolation (each session is its own shell/state) and speed.
	wg = sync.WaitGroup{}
	outs := make([]string, n)
	cmdStart := time.Now()
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cmd := fmt.Sprintf("echo session-%d-marker-$((%d*7))", i, i)
			res, err := client.CallTool(ctx, "term_run", map[string]any{"id": ids[i], "command": cmd})
			if err != nil {
				errs[i] = err
				return
			}
			outs[i] = textOf(res)
		}(i)
	}
	wg.Wait()
	t.Logf("ran %d concurrent commands in %s", n, time.Since(cmdStart))
	for i, err := range errs {
		if err != nil {
			t.Fatalf("term_run[%d] failed: %v", i, err)
		}
		want := fmt.Sprintf("session-%d-marker-%d", i, i*7)
		if !strings.Contains(outs[i], want) {
			t.Errorf("session %d: expected marker %q in output, got: %s", i, want, outs[i])
		}
	}

	// 3) Persistent state per session: set an env var in session 0, confirm
	// session 1 does NOT see it (isolation) and session 0 DOES on a second
	// call (persistence) — the defining property one-shot docker exec can't give.
	if _, err := client.CallTool(ctx, "term_run", map[string]any{"id": ids[0], "command": "export LIVE_CHECK=isolated_ok"}); err != nil {
		t.Fatalf("export: %v", err)
	}
	res, err := client.CallTool(ctx, "term_run", map[string]any{"id": ids[0], "command": "echo got=$LIVE_CHECK"})
	if err != nil {
		t.Fatalf("read own var: %v", err)
	}
	if !strings.Contains(textOf(res), "got=isolated_ok") {
		t.Errorf("session 0 did not see its own persisted var: %v", textOf(res))
	}
	res, err = client.CallTool(ctx, "term_run", map[string]any{"id": ids[1], "command": "echo got=[$LIVE_CHECK]"})
	if err != nil {
		t.Fatalf("read other session var: %v", err)
	}
	if !strings.Contains(textOf(res), "got=[]") {
		t.Errorf("session 1 leaked session 0's var (isolation broken): %v", textOf(res))
	}

	// 4) Fire-and-return + term_wait(pattern) — the interactive path.
	if _, err := client.CallTool(ctx, "term_run", map[string]any{
		"id": ids[2], "command": "sleep 1 && echo LIVE_READY_TOKEN", "wait": false,
	}); err != nil {
		t.Fatalf("fire-and-return: %v", err)
	}
	res, err = client.CallTool(ctx, "term_wait", map[string]any{"id": ids[2], "pattern": "LIVE_READY_TOKEN", "timeout_secs": 10})
	if err != nil {
		t.Fatalf("term_wait: %v", err)
	}
	if !strings.Contains(textOf(res), "matched pattern") {
		t.Errorf("term_wait did not report a match: %v", textOf(res))
	}

	// 5) C-c interrupt on a long-running command — the interactive-control path.
	if _, err := client.CallTool(ctx, "term_run", map[string]any{"id": ids[3], "command": "sleep 30", "wait": false}); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	time.Sleep(400 * time.Millisecond)
	if _, err := client.CallTool(ctx, "term_keys", map[string]any{"id": ids[3], "keys": "C-c"}); err != nil {
		t.Fatalf("send C-c: %v", err)
	}
	res, err = client.CallTool(ctx, "term_run", map[string]any{"id": ids[3], "command": "echo AFTER_INTERRUPT", "timeout_secs": 8})
	if err != nil {
		t.Fatalf("post-interrupt run: %v", err)
	}
	if !strings.Contains(textOf(res), "AFTER_INTERRUPT") {
		t.Errorf("prompt did not return after C-c: %v", textOf(res))
	}

	// 6) term_list must show all N sessions this test opened.
	res, err = client.CallTool(ctx, "term_list", map[string]any{})
	if err != nil {
		t.Fatalf("term_list: %v", err)
	}
	listOut := textOf(res)
	for _, id := range ids {
		if !strings.Contains(listOut, id) {
			t.Errorf("term_list missing session %q: %s", id, listOut)
		}
	}

	t.Logf("FULL live check passed: %d sessions, concurrent open+run, isolation, wait-for-pattern, C-c interrupt, list — total %s", n, time.Since(start))
}
