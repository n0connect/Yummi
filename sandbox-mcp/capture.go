package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// runCapturedToFile behaves exactly like runCapturedEnv, but when outputFile
// is non-empty also persists the exact same text the caller sees to that
// path. This is what lets a recon tool's result feed straight into a later
// step's file-based input (dnsx/alterx/nuclei's own -l flag, a follow-up
// grep/sort pipeline) without the model having to hand-copy potentially
// large tool output into a new file itself via a separate shell step. Using
// the text this function ALREADY captured — rather than trusting each tool's
// own native -o/-f flag, whose conventions vary (some tools mix a progress
// bar into the same stream a naive shell redirect would capture) — guarantees
// the saved file always matches what the model just read, tool by tool,
// with no separate flag-convention surface to get wrong. A save failure is
// reported inline in the returned text but never replaces or hides the
// tool's own captured output — the pentest command already ran and produced
// a real result regardless of whether the convenience save succeeded.
func runCapturedToFile(ctx context.Context, outputFile string, program string, args ...string) (*mcp.CallToolResult, any, error) {
	return runCapturedEnvToFile(ctx, nil, outputFile, program, args...)
}

// runCapturedEnvToFile is runCapturedToFile with the extra-environment layer
// runCapturedEnv itself supports — the one caller that needs both together is
// wpscanHandler (HOME redirected to a writable path, output also saved for a
// later step).
func runCapturedEnvToFile(ctx context.Context, extraEnv []string, outputFile string, program string, args ...string) (*mcp.CallToolResult, any, error) {
	result, meta, err := runCapturedEnv(ctx, extraEnv, program, args...)
	if outputFile == "" || result == nil || len(result.Content) == 0 {
		return result, meta, err
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		return result, meta, err
	}
	text.Text += saveNote(outputFile, text.Text)
	return result, meta, err
}

// sandboxHome is the pentester UID's own home directory (sandbox/Dockerfile's
// `useradd --create-home` for UID 1000) and the fixed backstop output_file
// writes are confined to. output_file is a value the model chooses, and the
// model's choice can in turn be shaped by hostile content in scanned-target
// responses (the exact content-layer prompt-injection risk the orchestrator's
// threatscan package exists for) — so this floor stops an injected or
// mistaken path from landing outside the pentester user's own scratch space,
// even though the sandbox's root filesystem is otherwise writable.
var sandboxHome = "/home/pentester" // overridden in tests, which can't write under the real path

// hasPathPrefix reports whether p is lexically at or inside base — a pure
// string/path comparison, no filesystem access. Both arguments must already
// be resolved to the same degree (either both raw, or both passed through
// filepath.EvalSymlinks) or the comparison is meaningless. Equality (p ==
// base) counts as inside: writeConfinedFile's second call site compares a
// file's parent directory against sandboxHome, and a file living directly in
// sandboxHome (parent == sandboxHome itself) is the ordinary, valid case —
// rejecting it here would just mean a WriteFile attempt against the home
// directory path itself, which the OS already refuses on its own.
func hasPathPrefix(p, base string) bool {
	rel, err := filepath.Rel(base, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// writeConfinedFile writes content to path after confirming path is an
// absolute path that resolves inside sandboxHome (see sandboxHome's doc
// comment for why). Shared by every tool that accepts a model-chosen output
// path — saveNote's append-to-captured-output style, and consolidate_hosts'
// direct single-shot write — so the confinement logic exists in exactly one
// place. Returns the cleaned absolute path and an empty reason on success,
// or an empty path and a human-readable (unwrapped — callers embed it in
// their own message format) reason otherwise.
func writeConfinedFile(path string, content []byte) (clean string, reason string) {
	if !filepath.IsAbs(path) {
		return "", "must be an absolute path"
	}
	clean = filepath.Clean(path)
	if !hasPathPrefix(clean, sandboxHome) {
		return "", fmt.Sprintf("must be under %s", sandboxHome)
	}
	dir := filepath.Dir(clean)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err.Error()
	}
	// dir may pre-exist as (or contain) a symlink planted by an earlier step;
	// re-resolve it, and sandboxHome the same way, before writing — comparing
	// a resolved dir against an unresolved sandboxHome would either falsely
	// reject (if sandboxHome itself sits behind a symlink) or, worse, falsely
	// accept an escape.
	realDir, dirErr := filepath.EvalSymlinks(dir)
	realHome, homeErr := filepath.EvalSymlinks(sandboxHome)
	if dirErr == nil && homeErr == nil && !hasPathPrefix(realDir, realHome) {
		return "", fmt.Sprintf("resolves outside %s", sandboxHome)
	}
	if err := os.WriteFile(clean, content, 0o600); err != nil {
		return "", err.Error()
	}
	return clean, ""
}

// saveNote writes content to path and returns a short status line to append
// to a tool's result text. path must be an absolute path under sandboxHome —
// sandbox-mcp's own working directory is fixed at /home/pentester regardless
// of whatever directory a separate shell command "cd"s into, so a relative
// path here would silently resolve against the wrong base.
func saveNote(path, content string) string {
	clean, reason := writeConfinedFile(path, []byte(content))
	if reason != "" {
		return fmt.Sprintf("\n\n[output_file %q was not saved: %s]", path, reason)
	}
	return fmt.Sprintf("\n\n[output saved to %s]", clean)
}
