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
	defaultTimeout   = 120 * time.Second
	maxOutputBytes   = 1 << 20 // 1 MiB
)

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

	var stdout, stderr bytes.Buffer
	stdout.Grow(4096)
	stderr.Grow(1024)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

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
		Stdout:   truncate(stdout.String(), maxOutputBytes),
		Stderr:   truncate(stderr.String(), maxOutputBytes),
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

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n... [truncated]"
}
