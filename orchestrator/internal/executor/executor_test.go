package executor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/n0connect/yummi/orchestrator/internal/execution"
	"github.com/n0connect/yummi/orchestrator/internal/gateway"
	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
	"github.com/n0connect/yummi/orchestrator/internal/sandbox"
)

func TestRecordTacticOutcome(t *testing.T) {
	ks, _ := knowledge.NewStore(nil)
	recordTacticOutcome(context.Background(), ks, "nmap -sV 10.0.0.5", true)
	recordTacticOutcome(context.Background(), ks, "gobuster dir -u http://10.0.0.5", false)

	worked := ks.ListTactics("nmap", 10)
	if len(worked) != 1 || worked[0].Outcome != "worked" {
		t.Fatalf("nmap tactic not recorded as worked: %+v", worked)
	}
	failed := ks.ListTactics("gobuster", 10)
	if len(failed) != 1 || failed[0].Outcome != "failed" {
		t.Fatalf("gobuster tactic not recorded as failed: %+v", failed)
	}
}

func TestRunScopedCommandBlocksOutOfScope(t *testing.T) {
	socket := gateway.StartTestServer(t, false, func(req gateway.Request) gateway.Response {
		if req.Type == "check_scope" {
			return gateway.Response{Type: "scope_check_result", IP: req.IP, Verdict: "block"}
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

func TestRunScopedCommandHoldsOnApprovalRequired(t *testing.T) {
	socket := gateway.StartTestServer(t, false, func(req gateway.Request) gateway.Response {
		if req.Type == "check_scope" {
			return gateway.Response{Type: "scope_check_result", IP: req.IP, Verdict: "needs_approval"}
		}
		return gateway.Response{Type: "error", Message: "unexpected request"}
	})
	pc := gateway.NewClient(socket)
	bridge := sandbox.NewBridge("yummi-sandbox-test-nonexistent")

	_, err := RunScopedCommand(context.Background(), bridge, pc, "nmap -sV 8.8.8.8")
	if err == nil {
		t.Fatal("expected an egress-approval-required error")
	}
	if !errors.Is(err, ErrEgressApprovalRequired) {
		t.Fatalf("expected ErrEgressApprovalRequired, got: %v", err)
	}
	if errors.Is(err, ErrOutOfScope) {
		t.Fatal("needs_approval should not be classified as ErrOutOfScope — it's a hold, not a hard block")
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

// TestRunScopedCommandSurfacesGatewayKernelWarning is a regression test: the
// gateway can accept a temporary-rule request (policy state updated) while
// its own internal nftables kernel-level unlock silently fails. Before this
// fix that failure was visible only in the gateway's own logs — the
// orchestrator believed the unlock fully succeeded. RunScopedCommand must
// carry the gateway's kernel_warning into the sandbox.Result the operator/API
// actually sees.
func TestRunScopedCommandSurfacesGatewayKernelWarning(t *testing.T) {
	const warning = "policy rule added but the nftables kernel unlock for 10.0.0.5/32 failed (no such device) — sandbox traffic to this target is still dropped"
	socket := gateway.StartTestServer(t, false, func(req gateway.Request) gateway.Response {
		switch req.Type {
		case "check_scope":
			return gateway.Response{Type: "scope_check_result", IP: req.IP, Verdict: "allow"}
		case "add_temporary_rule":
			return gateway.Response{Type: "rule_added", RuleID: "r1", ExpiresAt: 9999, KernelWarning: warning}
		default:
			return gateway.Response{Type: "error", Message: "unexpected request"}
		}
	})
	pc := gateway.NewClient(socket)
	bridge := sandbox.NewBridge("yummi-sandbox-test-nonexistent") // real bridge; docker exec fails harmlessly, we only assert the warning

	result, _ := RunScopedCommand(context.Background(), bridge, pc, "nmap -sV 10.0.0.5")
	if result == nil {
		t.Fatal("expected a non-nil result (docker exec ran and failed with an exit code, not a startup error)")
	}
	if result.Warning != warning {
		t.Fatalf("expected gateway kernel_warning to reach result.Warning, got %q", result.Warning)
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
	e := New(nil, nil, nil)
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
	e := New(sandbox.NewBridge("unused-container"), nil, nil)
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
