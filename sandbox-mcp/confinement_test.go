package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Uses the real process environment. The Go builder container must reject
// execution; the non-root Kali test container must permit it. No test doubles.
func TestConfinementActualEnvironment(t *testing.T) {
	err := requireSandboxRuntime()
	_, installed := os.Stat("/usr/local/bin/yummi-sandbox-mcp")
	if installed == nil && os.Geteuid() == 1000 {
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	if err == nil {
		t.Fatal("execution accepted outside the non-root Yummi sandbox")
	}
}

func TestConfinementRejectsProcessAndBrowserBeforeSideEffects(t *testing.T) {
	if requireSandboxRuntime() == nil {
		t.Skip("rejection test runs in the real builder container, not the Kali sandbox")
	}
	ctx := context.Background()
	result, _, err := runCaptured(ctx, "/bin/sh", "-c", "printf MUST_NOT_EXECUTE")
	if err != nil || result == nil || !result.IsError {
		t.Fatalf("process execution not blocked: result=%v err=%v", result, err)
	}
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok && strings.Contains(text.Text, "MUST_NOT_EXECUTE") {
			t.Fatal("shell executed outside sandbox")
		}
	}
	if _, err := runTmux(ctx, "-V"); err == nil || !strings.Contains(err.Error(), "sandbox runtime required") {
		t.Fatalf("tmux not rejected by confinement: %v", err)
	}
	driver := NewRodDriver()
	if err := driver.ensureBrowser(ctx, nil); err == nil || !strings.Contains(err.Error(), "sandbox runtime required") {
		t.Fatalf("browser not rejected by confinement: %v", err)
	}
}
