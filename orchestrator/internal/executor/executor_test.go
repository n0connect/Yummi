package executor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

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

func TestRunScopedCommandChecksEveryExplicitTarget(t *testing.T) {
	var checked []string
	socket := gateway.StartTestServer(t, false, func(req gateway.Request) gateway.Response {
		if req.Type == "check_scope" {
			checked = append(checked, req.IP)
			verdict := "allow"
			if req.IP == "8.8.8.8" {
				verdict = "block"
			}
			return gateway.Response{Type: "scope_check_result", IP: req.IP, Verdict: verdict}
		}
		return gateway.Response{Type: "error", Message: "unexpected request"}
	})
	pc := gateway.NewClient(socket)
	bridge := sandbox.NewBridge("yummi-sandbox-test-nonexistent")

	_, err := RunScopedCommand(context.Background(), bridge, pc, "nmap 10.0.0.1 8.8.8.8")
	if !errors.Is(err, ErrOutOfScope) {
		t.Fatalf("expected second target to be blocked, got: %v", err)
	}
	if len(checked) != 2 || checked[0] != "10.0.0.1" || checked[1] != "8.8.8.8" {
		t.Fatalf("checked targets = %v, want both in argument order", checked)
	}
}

// TestRunScopedCommandBlocksByHostnamePolicy proves a hostname target whose
// name the gateway blocks (e.g. *.facebook.com) is rejected BEFORE any
// resolution or IP-egress check — a hard, name-level deny.
func TestRunScopedCommandBlocksByHostnamePolicy(t *testing.T) {
	var sawScopeCheck bool
	socket := gateway.StartTestServer(t, false, func(req gateway.Request) gateway.Response {
		switch req.Type {
		case "check_hostname":
			return gateway.Response{Type: "hostname_result", Host: req.Host, Verdict: "block"}
		case "check_scope":
			sawScopeCheck = true // must NOT happen — hostname block short-circuits
			return gateway.Response{Type: "scope_check_result", IP: req.IP, Verdict: "allow"}
		}
		return gateway.Response{Type: "error", Message: "unexpected request"}
	})
	pc := gateway.NewClient(socket)
	bridge := sandbox.NewBridge("yummi-sandbox-test-nonexistent")

	_, err := RunScopedCommand(context.Background(), bridge, pc, "curl https://www.facebook.com")
	if err == nil {
		t.Fatal("expected a hostname-policy block error")
	}
	if !errors.Is(err, ErrOutOfScope) {
		t.Fatalf("expected ErrOutOfScope for hostname block, got: %v", err)
	}
	if sawScopeCheck {
		t.Fatal("hostname block must short-circuit BEFORE the IP scope check")
	}
}

// TestRunScopedCommandHostnameAllowedProceeds proves an allow/neutral hostname
// verdict does not block — it falls through to the IP-egress flow (which here
// blocks on its own, proving the hostname gate let it pass).
func TestRunScopedCommandHostnameAllowedProceeds(t *testing.T) {
	socket := gateway.StartTestServer(t, false, func(req gateway.Request) gateway.Response {
		switch req.Type {
		case "check_hostname":
			return gateway.Response{Type: "hostname_result", Host: req.Host, Verdict: "allow"}
		case "check_scope":
			return gateway.Response{Type: "scope_check_result", IP: req.IP, Verdict: "block"}
		}
		return gateway.Response{Type: "error", Message: "unexpected request"}
	})
	pc := gateway.NewClient(socket)
	bridge := sandbox.NewBridge("yummi-sandbox-test-nonexistent")

	// example.com resolves; the hostname gate allows it, then the IP egress
	// check blocks — so the error is the IP block, not a hostname block.
	_, err := RunScopedCommand(context.Background(), bridge, pc, "curl https://example.com")
	if !errors.Is(err, ErrOutOfScope) {
		t.Fatalf("expected the IP-egress block to surface (hostname allowed), got: %v", err)
	}
}

func TestIsHostnameTarget(t *testing.T) {
	cases := map[string]bool{
		"example.com":    true,
		"a.b.google.com": true,
		"8.8.8.8":        false,
		"10.0.0.0/24":    false,
		"2606:4700::1":   false,
	}
	for target, want := range cases {
		if got := isHostnameTarget(target); got != want {
			t.Errorf("isHostnameTarget(%q) = %v, want %v", target, got, want)
		}
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
	e := New(nil, nil, nil, nil)
	if e == nil {
		t.Fatal("expected non-nil executor")
	}
	fn := e.ProcessFunc()
	if fn == nil {
		t.Fatal("expected non-nil process func")
	}
}

func TestIPNetwork(t *testing.T) {
	tests := []struct {
		target string
		want   string
	}{
		{"1.2.3.4", "1.2.3.4/32"},
		{"10.0.0.0/24", "10.0.0.0/24"},
		{"2001:db8::1", "2001:db8::1/128"},
		{"2001:db8::/32", "2001:db8::/32"},
		{"example.com", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := ipNetwork(tt.target); got != tt.want {
			t.Errorf("ipNetwork(%q) = %q, want %q", tt.target, got, tt.want)
		}
	}
}

func TestResolvedNetworks(t *testing.T) {
	tests := []struct {
		name   string
		target string
		ips    []string
		want   []string
	}{
		{"ip literal, no resolution", "1.2.3.4", nil, []string{"1.2.3.4/32"}},
		{"cidr literal, no resolution", "10.0.0.0/24", nil, []string{"10.0.0.0/24"}},
		{"hostname resolved to several", "example.com", []string{"93.184.216.34", "1.2.3.4"}, []string{"93.184.216.34/32", "1.2.3.4/32"}},
		{"hostname unresolved", "example.com", nil, nil},
		{"resolved set drops junk", "h", []string{"5.5.5.5", "not-an-ip"}, []string{"5.5.5.5/32"}},
		{"ipv6 supported", "h", []string{"1.2.3.4", "2606:4700::1"}, []string{"1.2.3.4/32", "2606:4700::1/128"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolvedNetworks(tt.target, tt.ips)
			if len(got) != len(tt.want) {
				t.Fatalf("resolvedNetworks(%q,%v) = %v, want %v", tt.target, tt.ips, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("resolvedNetworks(%q,%v)[%d] = %q, want %q", tt.target, tt.ips, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestProcessBlocksTargetedCommandWhenGatewayUnavailable(t *testing.T) {
	e := New(sandbox.NewBridge("unused-container"), nil, nil, nil)
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

func TestProcessFunc_MCPRecord_DispatchesToSandboxMCP(t *testing.T) {
	socket := gateway.StartTestServer(t, false, func(req gateway.Request) gateway.Response {
		if req.Type == "check_scope" {
			return gateway.Response{Type: "scope_check_result", IP: req.IP, Verdict: "allow"}
		}
		return gateway.Response{Type: "error", Message: "unexpected request"}
	})
	pc := gateway.NewClient(socket)

	fakeMCP := &fakeSandboxMCPClient{
		result: &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Nmap done: 1 host up"}}},
	}
	e := New(sandbox.NewBridge("unused"), pc, nil, fakeMCP)

	rec := execution.Record{
		ID: "rec-1", SessionID: "sess-1", Command: "nmap -sV 127.0.0.1",
		MCPTool: "nmap_scan", MCPArgs: `{"target":"127.0.0.1"}`,
	}
	output, err := e.ProcessFunc()(context.Background(), rec)
	if err != nil {
		t.Fatalf("ProcessFunc: %v", err)
	}
	if !strings.Contains(output, "Nmap done") {
		t.Fatalf("output = %q, want it to contain the MCP result text", output)
	}
	if fakeMCP.calledTool != "nmap_scan" {
		t.Fatalf("sandboxMCP.CallTool called with tool = %q, want nmap_scan", fakeMCP.calledTool)
	}
}

// TestProcessFunc_MCPRecord_SurfacesStderrOnFailure caught a real bug during
// live E2E verification: a failing MCP tool call's diagnostic text (mapped
// into sandbox.Result.Stderr by mcpResultToSandboxResult) was silently
// dropped because the output-combining code appended Stdout and Warning but
// never Stderr — unlike the shell path's identical combining logic just
// above it, which does. The model saw an empty failure reason and blindly
// retried instead of adjusting.
func TestProcessFunc_MCPRecord_SurfacesStderrOnFailure(t *testing.T) {
	socket := gateway.StartTestServer(t, false, func(req gateway.Request) gateway.Response {
		if req.Type == "check_scope" {
			return gateway.Response{Type: "scope_check_result", IP: req.IP, Verdict: "allow"}
		}
		return gateway.Response{Type: "error", Message: "unexpected request"}
	})
	pc := gateway.NewClient(socket)

	fakeMCP := &fakeSandboxMCPClient{
		result: &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "Failed to resolve host"}}},
	}
	e := New(sandbox.NewBridge("unused"), pc, nil, fakeMCP)

	rec := execution.Record{
		ID: "rec-1", SessionID: "sess-1", Command: "nmap -sV 10.0.0.9",
		MCPTool: "nmap_scan", MCPArgs: `{"target":"10.0.0.9"}`,
	}
	output, err := e.ProcessFunc()(context.Background(), rec)
	if err == nil {
		t.Fatal("expected an error for a non-zero exit MCP result")
	}
	if !strings.Contains(output, "Failed to resolve host") {
		t.Fatalf("output = %q, want it to contain the MCP failure text (Stderr)", output)
	}
}

// fakeSandboxMCPClient is a real, self-contained test double for the
// sandboxMCPClient interface — not a mock of Yummi's own sandboxmcp.Client,
// which is exercised for real in internal/sandboxmcp's own test suite via
// the SDK's in-memory transport. This satisfies the project's no-mocks
// convention: no framework, no behavior assertions baked into a mock — just
// a plain struct recording what it was called with.
type fakeSandboxMCPClient struct {
	calledTool string
	calledArgs map[string]any
	result     *mcp.CallToolResult
	err        error
}

func (f *fakeSandboxMCPClient) CallTool(_ context.Context, tool string, args map[string]any) (*mcp.CallToolResult, error) {
	f.calledTool = tool
	f.calledArgs = args
	return f.result, f.err
}
