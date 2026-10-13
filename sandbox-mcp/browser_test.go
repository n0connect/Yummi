package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func liveChromiumAvailable() bool {
	if requireSandboxRuntime() != nil {
		return false
	}
	if _, err := os.Stat("/usr/bin/chromium"); err == nil {
		return true
	}
	return false
}

func TestBrowserToolsRegistration(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v1"}, nil)
	registerTools(server)
}

func TestBrowserOpenValidation(t *testing.T) {
	ctx := context.Background()

	// Empty URL must be rejected
	res, _, _ := browserOpenHandler(ctx, nil, browserOpenArgs{URL: ""})
	if res == nil || !res.IsError {
		t.Error("expected error for empty URL")
	}

	// Flag-like target must be rejected (security injection guard)
	res, _, _ = browserOpenHandler(ctx, nil, browserOpenArgs{URL: "--remote-debugging-port=9999"})
	if res == nil || !res.IsError {
		t.Error("expected error for flag-like URL")
	}
}

func TestBrowserInteractValidation(t *testing.T) {
	ctx := context.Background()

	// Empty action must be rejected
	res, _, _ := browserInteractHandler(ctx, nil, browserInteractArgs{Action: ""})
	if res == nil || !res.IsError {
		t.Error("expected error for empty action")
	}
}

func TestBrowserStorageValidation(t *testing.T) {
	ctx := context.Background()

	// Empty action must be rejected
	res, _, _ := browserStorageHandler(ctx, nil, browserStorageArgs{Action: ""})
	if res == nil || !res.IsError {
		t.Error("expected error for empty storage action")
	}
}

func TestBrowserTabsValidation(t *testing.T) {
	ctx := context.Background()

	// Empty action must be rejected
	res, _, _ := browserTabsHandler(ctx, nil, browserTabsArgs{Action: ""})
	if res == nil || !res.IsError {
		t.Error("expected error for empty tab action")
	}
}

func TestLive_RodDriver_RealDOMExecution(t *testing.T) {
	if !liveChromiumAvailable() {
		t.Skip("Chromium binary not installed in test environment — skipping live browser test")
	}

	driver := NewRodDriver()
	defer func() { _ = driver.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), contextTimeout())
	defer cancel()

	// Test real navigation against inline HTML data URL — no external network needed
	dataURL := "data:text/html,<html><head><title>Yummi Pwnbox Test</title></head><body><h1>Login Portal</h1><form><input type='text' id='user' name='username' placeholder='Username'><input type='password' id='pass' name='password'><button type='submit' id='submit-btn'>Sign In</button></form></body></html>"

	info, err := driver.Open(ctx, OpenParams{
		URL:       dataURL,
		WaitUntil: "load",
		Session:   "test-session",
	})
	if err != nil {
		t.Fatalf("real driver Open failed: %v", err)
	}
	if info.Title != "Yummi Pwnbox Test" {
		t.Errorf("title = %q, want Yummi Pwnbox Test", info.Title)
	}

	// Test real snapshot generation
	snap, err := driver.Snapshot(ctx, true, true, 4, "")
	if err != nil {
		t.Fatalf("real driver Snapshot failed: %v", err)
	}
	if !strings.Contains(snap.TreeText, "Username") || !strings.Contains(snap.TreeText, "@e") {
		t.Errorf("snapshot missing elements: %s", snap.TreeText)
	}

	// Test real fill interaction
	interactRes, err := driver.Interact(ctx, InteractParams{
		Action: "fill",
		Ref:    "@e1",
		Value:  "admin",
	})
	if err != nil {
		t.Fatalf("real driver Interact(fill) failed: %v", err)
	}
	if !interactRes.Success {
		t.Errorf("interact fill failed: %+v", interactRes)
	}

	// Test real JavaScript evaluation in page context
	evalVal, err := driver.Eval(ctx, "document.getElementById('user').value")
	if err != nil {
		t.Fatalf("real driver Eval failed: %v", err)
	}
	if evalVal != "admin" {
		t.Errorf("evalVal = %v, want admin", evalVal)
	}

	// Test set_viewport action
	vpRes, err := driver.Interact(ctx, InteractParams{
		Action: "set_viewport",
		Width:  1280,
		Height: 720,
	})
	if err != nil || !vpRes.Success {
		t.Errorf("set_viewport failed: %v", err)
	}

	// Test clipboard action
	cbRes, err := driver.Interact(ctx, InteractParams{
		Action: "clipboard_write",
		Value:  "secret_auth_token",
	})
	if err != nil || !cbRes.Success {
		t.Errorf("clipboard_write failed: %v", err)
	}
}

func contextTimeout() time.Duration {
	return 30 * time.Second
}
