package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	maxOutputBytes = 1 << 20 // 1 MiB, mirrors orchestrator's sandbox.Bridge cap
	truncationMark = "\n... [truncated]"
	execTimeout    = 10 * time.Minute
)

// cappedBuffer accepts the complete io.Writer contract while retaining at
// most limit bytes — bounds memory during execution rather than buffering
// unbounded output and truncating after the fact.
type cappedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func newCappedBuffer(limit int) *cappedBuffer {
	b := &cappedBuffer{limit: limit}
	b.buf.Grow(min(limit, 4096))
	return b
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - b.buf.Len()
	if remaining > 0 {
		keep := min(remaining, n)
		_, _ = b.buf.Write(p[:keep])
	}
	if n > remaining {
		b.truncated = true
	}
	return n, nil
}

func (b *cappedBuffer) String() string {
	if b.truncated {
		return b.buf.String() + truncationMark
	}
	return b.buf.String()
}

// errResult builds an MCP tool-execution failure result (not a Go error —
// a tool that ran and failed is a normal MCP result per spec, distinct from
// a protocol-level error).
func errResult(msg string) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}, nil, nil
}

// runCaptured execs program with argv directly — never a shell, so there is
// no metacharacter injection surface. A non-zero exit or a launch failure
// (binary not found) both become an MCP IsError result; the returned Go
// error is reserved for something runCaptured itself cannot express as a
// result (there is none today — kept for signature symmetry with AddTool's
// handler shape).
func runCaptured(ctx context.Context, program string, args ...string) (*mcp.CallToolResult, any, error) {
	return runCapturedEnv(ctx, nil, program, args...)
}

// runCapturedEnv is runCaptured with additional environment variables layered
// on top of the inherited environment (each entry overrides any existing var
// of the same name, matching os/exec.Cmd.Env's "last wins" semantics). Exists
// for the rare tool that needs a per-invocation override — e.g. wpscan writes
// its vulnerability-database cache under $HOME/.cache, which fails outright
// under the sandbox's read-only root filesystem unless HOME is redirected
// somewhere writable (/tmp is a tmpfs mount, unaffected by the read-only
// root) — rather than a global os.Setenv, which would race across this
// server's concurrently-handled tool calls.
func runCapturedEnv(ctx context.Context, extraEnv []string, program string, args ...string) (*mcp.CallToolResult, any, error) {
	ctx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, program, args...)
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	stdout := newCappedBuffer(maxOutputBytes)
	stderr := newCappedBuffer(maxOutputBytes)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	runErr := cmd.Run()
	out := stdout.String()
	if stderr.String() != "" {
		if out != "" {
			out += "\n--- stderr ---\n"
		}
		out += stderr.String()
	}
	if runErr != nil {
		if out == "" {
			out = runErr.Error()
		}
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: out}}}, nil, nil
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: out}}}, nil, nil
}
