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

func TestTruncate(t *testing.T) {
	short := "hello"
	if truncate(short, 10) != short {
		t.Fatal("short string should not be truncated")
	}
	long := strings.Repeat("x", 100)
	result := truncate(long, 50)
	if len(result) <= 50 {
		t.Fatal("truncated result should include suffix")
	}
	if !strings.Contains(result, "[truncated]") {
		t.Fatal("truncated result should contain marker")
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
