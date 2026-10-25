package main

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// These tests exercise burp_bridge.go's own networking/validation logic
// against real TCP listeners standing in for Burp's SSE server — not a real
// burpsuite process. Burp Suite has no documented headless mode (see the
// package comment in burp_bridge.go), so actually launching it needs a real
// X display this test environment doesn't provide; that launch path is
// verified by hand against the built sandbox image instead.

func TestDisplayOrDefault(t *testing.T) {
	t.Setenv("DISPLAY", "")
	if got := displayOrDefault(); got != ":1" {
		t.Fatalf("displayOrDefault() = %q, want :1 when DISPLAY is unset", got)
	}
	t.Setenv("DISPLAY", ":42")
	if got := displayOrDefault(); got != ":42" {
		t.Fatalf("displayOrDefault() = %q, want :42", got)
	}
}

func TestBurpPortReachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	t.Setenv("YUMMI_BURP_MCP_PORT", port)

	if !burpPortReachable(2 * time.Second) {
		t.Fatal("expected the listener to be reachable")
	}
}

func TestBurpPortReachable_NothingListening(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	ln.Close()
	t.Setenv("YUMMI_BURP_MCP_PORT", port)

	if burpPortReachable(200 * time.Millisecond) {
		t.Fatal("expected nothing to be reachable on a closed port")
	}
}

func TestWaitForBurpPort_PicksUpLateListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_, port, _ := net.SplitHostPort(addr)
	ln.Close()
	t.Setenv("YUMMI_BURP_MCP_PORT", port)

	go func() {
		time.Sleep(300 * time.Millisecond)
		ln2, err := net.Listen("tcp", addr)
		if err != nil {
			return
		}
		defer ln2.Close()
		time.Sleep(3 * time.Second)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if !waitForBurpPort(ctx, 3*time.Second) {
		t.Fatal("expected waitForBurpPort to detect the delayed listener")
	}
}

func TestWaitForBurpPort_TimesOutWhenNothingListens(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	ln.Close()
	t.Setenv("YUMMI_BURP_MCP_PORT", port)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	if waitForBurpPort(ctx, 1*time.Second) {
		t.Fatal("expected waitForBurpPort to fail when nothing ever listens")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("waitForBurpPort should respect its timeout, took %s", elapsed)
	}
}

func TestBurpCallTool_RequiresToolName(t *testing.T) {
	session := newTestServer(t)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "burp_call_tool", Arguments: map[string]any{"tool_name": ""},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError for an empty tool_name")
	}
}
