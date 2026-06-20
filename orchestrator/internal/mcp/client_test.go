package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// echoServerEnv gates whether TestMain runs in echo-server mode. When set,
// the test binary acts as a tiny MCP JSON-RPC 2.0 server over stdio that
// echoes every call's params back as the result. This lets us avoid shipping
// a second binary just to test the client.
const echoServerEnv = "YUMMI_MCP_ECHO_SERVER"

func TestMain(m *testing.M) {
	if os.Getenv(echoServerEnv) == "1" {
		runEchoServer()
		return
	}
	os.Exit(m.Run())
}

// runEchoServer implements a trivial MCP-style stdio server used only by
// tests. It reads newline-delimited JSON requests, echoes params as result.
func runEchoServer() {
	scanner := bufio.NewScanner(os.Stdin)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		if req.Method == "" {
			continue
		}

		// Respond to a few known methods.
		switch req.Method {
		case "shutdown":
			return
		case "error":
			resp := Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &Error{Code: -32000, Message: "forced error"},
			}
			if data, err := json.Marshal(resp); err == nil {
				fmt.Fprintln(os.Stdout, string(data))
			}
		default:
			// Echo the params as the result.
			result := req.Params
			if len(result) == 0 {
				result = json.RawMessage(`null`)
			}
			resp := Response{JSONRPC: "2.0", ID: req.ID, Result: result}
			if data, err := json.Marshal(resp); err == nil {
				fmt.Fprintln(os.Stdout, string(data))
			}
		}
	}
}

// newEchoClient spawns the current test binary in echo-server mode.
func newEchoClient(t *testing.T) *Client {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	// Sanity: ensure the binary is runnable with the echo-server env var.
	// A TestMain in the same package handles the redirect.
	c, err := NewClient(Options{
		Command: []string{exe, "-test.run=^$"},
		Env:     []string{echoServerEnv + "=1"},
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestClientCallRoundTrip(t *testing.T) {
	// Quick pre-flight check so we don't leave zombies when exec is restricted.
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("shell not available in this sandbox")
	}

	c := newEchoClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	type payload struct {
		Name string `json:"name"`
	}
	raw, err := c.Call(ctx, "ping", payload{Name: "yummi"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var got payload
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Name != "yummi" {
		t.Errorf("expected echoed name=yummi, got %q", got.Name)
	}
}

func TestClientCallNilParams(t *testing.T) {
	c := newEchoClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	raw, err := c.Call(ctx, "ping", nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if string(raw) != "null" {
		t.Errorf("expected null result, got %s", string(raw))
	}
}

func TestClientCallServerError(t *testing.T) {
	c := newEchoClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := c.Call(ctx, "error", nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	var me *Error
	if !errorsAs(err, &me) {
		t.Fatalf("expected *mcp.Error, got %T: %v", err, err)
	}
	if me.Code != -32000 {
		t.Errorf("expected code -32000, got %d", me.Code)
	}
}

func TestClientContextCancellation(t *testing.T) {
	c := newEchoClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before the call even starts

	_, err := c.Call(ctx, "ping", nil)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

func TestClientNoCommand(t *testing.T) {
	_, err := NewClient(Options{})
	if err == nil {
		t.Error("expected error for empty command")
	}
}

func TestClientDoubleClose(t *testing.T) {
	c := newEchoClient(t)
	if err := c.Close(); err != nil {
		// The echo server exits when stdin closes; "signal: killed" is fine.
		t.Logf("first close returned: %v (ok)", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("second close should be no-op, got %v", err)
	}
}

// errorsAs is a tiny wrapper so we don't pull in the errors package in
// every test. Behaves like errors.As.
func errorsAs(err error, target any) bool {
	switch t := target.(type) {
	case **Error:
		if e, ok := err.(*Error); ok {
			*t = e
			return true
		}
	}
	return false
}
