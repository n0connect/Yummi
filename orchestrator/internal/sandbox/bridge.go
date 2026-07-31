// Package sandbox provides a bridge to execute commands inside the sandbox Kali container.
package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const (
	defaultContainer = "yummi-sandbox"
	// defaultTimeout bounds a single command's wall-clock time. Real pentest
	// tooling routinely exceeds a couple of minutes — a thorough nikto run or
	// an nmap -A sweep across several hosts commonly takes 5-20+ minutes — so
	// this is set generously rather than aggressively; SetTimeout overrides it
	// per-Bridge (wired to YUMMI_SANDBOX_EXEC_TIMEOUT in cmd/orchestrator) for
	// operators who need it shorter (fast dev iteration) or longer still.
	defaultTimeout = 10 * time.Minute
	maxOutputBytes = 1 << 20 // 1 MiB
	truncationMark = "\n... [truncated]"
)

// cappedBuffer accepts the complete writer contract while retaining at most
// limit bytes. This bounds memory during execution instead of first buffering
// an unbounded stream and truncating it after the process exits.
type cappedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func newCappedBuffer(limit int) *cappedBuffer {
	if limit < 0 {
		limit = 0
	}
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

// Bridge provides command execution inside the sandbox Docker container.
type Bridge struct {
	container string
	timeout   time.Duration
}

// Result holds the outcome of a command execution.
type Result struct {
	Command  string `json:"command"`
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	Duration int64  `json:"duration_ms"`
	// Warning carries a non-fatal, operator-visible note about the command's
	// surrounding infrastructure (e.g. the gateway's nftables unlock failed)
	// that did not block execution but may explain an otherwise-unexplained
	// network failure. Empty when nothing to report.
	Warning string `json:"warning,omitempty"`
}

// NewBridge creates a bridge to the named container.
func NewBridge(container string) *Bridge {
	if container == "" {
		container = defaultContainer
	}
	return &Bridge{
		container: container,
		timeout:   defaultTimeout,
	}
}

// Exec runs a command inside the sandbox container via docker exec.
// The command runs as the pentester user (uid 1000) in a non-interactive shell.
func (b *Bridge) Exec(ctx context.Context, command string) (*Result, error) {
	if command == "" {
		return nil, fmt.Errorf("empty command")
	}

	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()

	args := []string{
		"exec",
		"--user", "1000:1000",
		b.container,
		"/bin/bash", "-lc", command,
	}

	cmd := exec.CommandContext(ctx, "docker", args...)

	stdout := newCappedBuffer(maxOutputBytes)
	stderr := newCappedBuffer(maxOutputBytes)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start).Milliseconds()

	exitCode := 0
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr == context.DeadlineExceeded {
			return &Result{
				Command:  command,
				ExitCode: -1,
				Stderr:   "execution timed out",
				Duration: elapsed,
			}, fmt.Errorf("command timed out after %v", b.timeout)
		} else if ctxErr == context.Canceled {
			return &Result{
				Command:  command,
				ExitCode: -1,
				Stderr:   "execution canceled",
				Duration: elapsed,
			}, fmt.Errorf("command canceled")
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("docker exec failed: %w", err)
		}
	}

	return &Result{
		Command:  command,
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: elapsed,
	}, nil
}

// Ping checks if the sandbox container is reachable.
func (b *Bridge) Ping(ctx context.Context) error {
	result, err := b.Exec(ctx, "echo pong")
	if err != nil {
		return fmt.Errorf("sandbox ping failed: %w", err)
	}
	if !strings.Contains(result.Stdout, "pong") {
		return fmt.Errorf("sandbox ping: unexpected output: %s", result.Stdout)
	}
	return nil
}

// ContainerName returns the target container name.
func (b *Bridge) ContainerName() string {
	return b.container
}

// SetTimeout overrides the per-command execution timeout (default
// defaultTimeout). A zero or negative value is ignored, leaving the current
// timeout in place — callers pass an unvalidated config duration, and silently
// keeping a sane default beats a config typo turning into a zero-timeout
// (every command failing instantly).
func (b *Bridge) SetTimeout(d time.Duration) {
	if d > 0 {
		b.timeout = d
	}
}
