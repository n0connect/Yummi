package executor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/n0connect/yummi/orchestrator/internal/execution"
	"github.com/n0connect/yummi/orchestrator/internal/gateway"
	"github.com/n0connect/yummi/orchestrator/internal/sandbox"
)

func TestRunScopedCommandBlocksOutOfScope(t *testing.T) {
	socket := gateway.StartTestServer(t, false, func(req gateway.Request) gateway.Response {
		if req.Type == "check_scope" {
			return gateway.Response{Type: "scope_check_result", IP: req.IP, InScope: false}
		}
		return gateway.Response{Type: "error", Message: "unexpected request"}
	})
	pc := gateway.NewClient(socket)
	bridge := sandbox.NewBridge("yummi-sandbox-test-nonexistent")

	_, err := RunScopedCommand(context.Background(), bridge, pc, "nmap -sV 8.8.8.8")
	if err == nil {
		t.Fatal("expected an out-of-scope error")
	}
	if !errors.Is(err, ErrOutOfScope) {
		t.Fatalf("expected ErrOutOfScope, got: %v", err)
	}
}

func TestRunScopedCommandFailsClosedWhenGatewayUnreachable(t *testing.T) {
	pc := gateway.NewClient("/nonexistent/socket")
	bridge := sandbox.NewBridge("yummi-sandbox-test-nonexistent")

	_, err := RunScopedCommand(context.Background(), bridge, pc, "nmap -sV 8.8.8.8")
	if err == nil {
		t.Fatal("expected an error when gateway is unreachable (fail-closed)")
	}
	if errors.Is(err, ErrOutOfScope) {
		t.Fatal("gateway-unreachable should not be classified as ErrOutOfScope — it's a different failure mode")
	}
}

func TestRunScopedCommandRejectsNilBridge(t *testing.T) {
	pc := gateway.NewClient("/nonexistent/socket")
	_, err := RunScopedCommand(context.Background(), nil, pc, "echo hi")
	if err == nil {
		t.Fatal("expected error for nil bridge")
	}
}

func TestNewExecutor(t *testing.T) {
	e := New(nil, nil)
	if e == nil {
		t.Fatal("expected non-nil executor")
	}
	fn := e.ProcessFunc()
	if fn == nil {
		t.Fatal("expected non-nil process func")
	}
}

func TestHostNetwork(t *testing.T) {
	tests := []struct {
		target string
		want   string
	}{
		{"1.2.3.4", "1.2.3.4/32"},
		{"10.0.0.0/24", "10.0.0.0/24"},
		{"2001:db8::1", "2001:db8::1/128"},
		{"example.com", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := hostNetwork(tt.target); got != tt.want {
			t.Errorf("hostNetwork(%q) = %q, want %q", tt.target, got, tt.want)
		}
	}
}

func TestProcessBlocksTargetedCommandWhenGatewayUnavailable(t *testing.T) {
	e := New(sandbox.NewBridge("unused-container"), nil)
	_, err := e.ProcessFunc()(context.Background(), execution.Record{
		ID:        "exec-1",
		SessionID: "s1",
		Command:   "nmap -sV 10.0.0.1",
	})
	if err == nil {
		t.Fatal("expected gateway unavailable error")
	}
	if !strings.Contains(err.Error(), "gateway scope check unavailable") {
		t.Fatalf("expected gateway unavailable error, got %v", err)
	}
}
