package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// These tests drive a REAL tmux server (no mocks — per project rule the tool
// under test is exercised against the real dependency). They self-skip when
// tmux is not installed, matching the codebase's live-harness pattern.

func skipIfNoTmux(t *testing.T) {
	t.Helper()
	if !tmuxAvailable() {
		t.Skip("tmux not installed — skipping terminal-session integration tests")
	}
}

// resultText pulls the text payload out of an MCP tool result, failing the
// test if the tool reported an error result.
func resultText(t *testing.T, res *mcp.CallToolResult, err error) string {
	t.Helper()
	if err != nil {
		t.Fatalf("handler returned protocol error: %v", err)
	}
	if res == nil || len(res.Content) == 0 {
		t.Fatalf("empty result")
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected text content, got %T", res.Content[0])
	}
	if res.IsError {
		t.Fatalf("tool reported error: %s", tc.Text)
	}
	return tc.Text
}

// openTempSession opens a uniquely-named session and registers cleanup.
func openTempSession(t *testing.T, ctx context.Context) string {
	t.Helper()
	label := fmt.Sprintf("test_%s", nonceHex())
	res, _, err := termOpenHandler(ctx, nil, termOpenArgs{Label: label})
	resultText(t, res, err)
	t.Cleanup(func() {
		_, _, _ = termCloseHandler(context.Background(), nil, termIDArg{ID: label})
	})
	return label
}

func TestTerminal_RunCommandWaitsAndReturnsExit(t *testing.T) {
	skipIfNoTmux(t)
	ctx := context.Background()
	id := openTempSession(t, ctx)

	res, _, err := termRunHandler(ctx, nil, termRunArgs{ID: id, Command: "echo hello-yummi"})
	out := resultText(t, res, err)

	if !strings.Contains(out, "exit=0") {
		t.Errorf("expected exit=0, got: %q", out)
	}
	if !strings.Contains(out, "hello-yummi") {
		t.Errorf("expected command output in result, got: %q", out)
	}
	if strings.Contains(out, "__YUMMI_") {
		t.Errorf("sentinel markers leaked into output: %q", out)
	}
}

func TestTerminal_NonZeroExitCaptured(t *testing.T) {
	skipIfNoTmux(t)
	ctx := context.Background()
	id := openTempSession(t, ctx)

	res, _, err := termRunHandler(ctx, nil, termRunArgs{ID: id, Command: "sh -c 'exit 7'"})
	out := resultText(t, res, err)
	if !strings.Contains(out, "exit=7") {
		t.Errorf("expected exit=7, got: %q", out)
	}
}

// The defining property: a session is a persistent shell, so state set by one
// command is visible to the next. This is what the one-shot capture.go path
// cannot do.
func TestTerminal_SessionStatePersistsAcrossRuns(t *testing.T) {
	skipIfNoTmux(t)
	ctx := context.Background()
	id := openTempSession(t, ctx)

	if _, _, err := termRunHandler(ctx, nil, termRunArgs{ID: id, Command: "YUMMI_MARKER=persisted_ok"}); err != nil {
		t.Fatalf("set var: %v", err)
	}
	res, _, err := termRunHandler(ctx, nil, termRunArgs{ID: id, Command: "echo value=$YUMMI_MARKER"})
	out := resultText(t, res, err)
	if !strings.Contains(out, "value=persisted_ok") {
		t.Errorf("expected persisted env var, got: %q", out)
	}
}

// Fire-and-return + term_wait(pattern) is the interactive path: a command that
// produces output later is observed by waiting for a pattern, not by blocking.
func TestTerminal_FireAndWaitForPattern(t *testing.T) {
	skipIfNoTmux(t)
	ctx := context.Background()
	id := openTempSession(t, ctx)

	no := false
	res, _, err := termRunHandler(ctx, nil, termRunArgs{ID: id, Command: "sleep 1 && echo READY_TOKEN", Wait: &no})
	resultText(t, res, err) // fire-and-return acknowledged

	res, _, err = termWaitHandler(ctx, nil, termWaitArgs{ID: id, Pattern: "READY_TOKEN", TimeoutSecs: 10})
	out := resultText(t, res, err)
	if !strings.Contains(out, "matched pattern") || !strings.Contains(out, "READY_TOKEN") {
		t.Errorf("expected pattern match on READY_TOKEN, got: %q", out)
	}
}

// term_keys C-c must interrupt a running command so the prompt returns — the
// "etkileşim" verb. Proven by a subsequent waiting run completing quickly.
func TestTerminal_KeysInterruptRunningCommand(t *testing.T) {
	skipIfNoTmux(t)
	ctx := context.Background()
	id := openTempSession(t, ctx)

	no := false
	if _, _, err := termRunHandler(ctx, nil, termRunArgs{ID: id, Command: "sleep 60", Wait: &no}); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	if _, _, err := termKeysHandler(ctx, nil, termKeysArgs{ID: id, Keys: "C-c"}); err != nil {
		t.Fatalf("send C-c: %v", err)
	}
	time.Sleep(300 * time.Millisecond)

	res, _, err := termRunHandler(ctx, nil, termRunArgs{ID: id, Command: "echo AFTER_INTERRUPT", TimeoutSecs: 8})
	out := resultText(t, res, err)
	if !strings.Contains(out, "exit=0") || !strings.Contains(out, "AFTER_INTERRUPT") {
		t.Errorf("prompt did not return after C-c (interrupt failed), got: %q", out)
	}
}

func TestTerminal_ListAndClose(t *testing.T) {
	skipIfNoTmux(t)
	ctx := context.Background()
	id := openTempSession(t, ctx)

	res, _, err := termListHandler(ctx, nil, struct{}{})
	out := resultText(t, res, err)
	if !strings.Contains(out, id) {
		t.Errorf("expected session %q in list, got: %q", id, out)
	}

	res, _, err = termCloseHandler(ctx, nil, termIDArg{ID: id})
	resultText(t, res, err)

	// After close, a run against it must fail cleanly.
	res, _, _ = termRunHandler(ctx, nil, termRunArgs{ID: id, Command: "echo x"})
	if res == nil || !res.IsError {
		t.Errorf("expected error running against closed session")
	}
}

func TestTerminal_RejectsInvalidID(t *testing.T) {
	skipIfNoTmux(t)
	ctx := context.Background()
	for _, bad := range []string{"-flag", "has space", "dot.name", "a:b", strings.Repeat("x", 50)} {
		res, _, _ := termRunHandler(ctx, nil, termRunArgs{ID: bad, Command: "echo x"})
		if res == nil || !res.IsError {
			t.Errorf("expected rejection of invalid id %q", bad)
		}
	}
}

func TestTerminal_DuplicateOpenRejected(t *testing.T) {
	skipIfNoTmux(t)
	ctx := context.Background()
	id := openTempSession(t, ctx)
	res, _, _ := termOpenHandler(ctx, nil, termOpenArgs{Label: id})
	if res == nil || !res.IsError {
		t.Errorf("expected duplicate open of %q to be rejected", id)
	}
}
