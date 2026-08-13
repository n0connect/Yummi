package sandbox

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestNewBridge(t *testing.T) {
	b := NewBridge("")
	if b.container != defaultContainer {
		t.Fatalf("expected %s, got %s", defaultContainer, b.container)
	}
	b2 := NewBridge("custom")
	if b2.container != "custom" {
		t.Fatalf("expected custom, got %s", b2.container)
	}
}

// TestSetTimeout proves the zero-guard: real pentest tooling needs the
// timeout raised well past the default, but a config parse failure landing
// on the zero value must never silently turn every command instant-fail.
func TestSetTimeout(t *testing.T) {
	b := NewBridge("test-container")
	if b.timeout != defaultTimeout {
		t.Fatalf("expected default timeout %v, got %v", defaultTimeout, b.timeout)
	}
	b.SetTimeout(30 * time.Minute)
	if b.timeout != 30*time.Minute {
		t.Fatalf("expected overridden timeout 30m, got %v", b.timeout)
	}
	b.SetTimeout(0)
	if b.timeout != 30*time.Minute {
		t.Fatalf("SetTimeout(0) must be a no-op, got %v", b.timeout)
	}
	b.SetTimeout(-5 * time.Second)
	if b.timeout != 30*time.Minute {
		t.Fatalf("SetTimeout(negative) must be a no-op, got %v", b.timeout)
	}
}

func TestCappedBuffer(t *testing.T) {
	b := newCappedBuffer(50)
	if n, err := b.Write([]byte("hello")); err != nil || n != 5 {
		t.Fatalf("first write = (%d, %v), want (5, nil)", n, err)
	}
	long := strings.Repeat("x", 100)
	if n, err := b.Write([]byte(long)); err != nil || n != len(long) {
		t.Fatalf("overflow write = (%d, %v), want (%d, nil)", n, err, len(long))
	}
	if b.buf.Len() != 50 {
		t.Fatalf("retained %d bytes, want hard cap 50", b.buf.Len())
	}
	if !strings.HasSuffix(b.String(), truncationMark) {
		t.Fatalf("result should end with truncation marker: %q", b.String())
	}
}

func TestExecEmptyCommand(t *testing.T) {
	b := NewBridge("test-container")
	_, err := b.Exec(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty command")
	}
}

// Integration test — only runs when sandbox container is available.
func TestExecIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	b := NewBridge("yummi-sandbox")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Test ping
	if err := b.Ping(ctx); err != nil {
		t.Skipf("sandbox container not available: %v", err)
	}

	// Test command execution
	result, err := b.Exec(ctx, "nmap --version")
	if err != nil {
		t.Fatalf("exec failed: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected exit 0, got %d: %s", result.ExitCode, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "Nmap") {
		t.Fatalf("expected nmap version output, got: %s", result.Stdout)
	}
	if result.Duration <= 0 {
		t.Fatal("duration should be positive")
	}
}
