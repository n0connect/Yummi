package executor

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/n0connect/yummi/orchestrator/internal/execution"
	"github.com/n0connect/yummi/orchestrator/internal/gateway"
	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
	"github.com/n0connect/yummi/orchestrator/internal/sandbox"
)

// Procedural memory learns from successes only. The executor has no failed-
// tactic path any more, so a dead-end command can never be written, and the
// target is scrubbed on the way in.
func TestRecordWorkedTacticLearnsOnlyFromSuccess(t *testing.T) {
	ks, _ := knowledge.NewStore(nil)
	recordWorkedTactic(context.Background(), ks, "nmap -sV 10.0.0.5")
	recordWorkedTactic(context.Background(), ks, "") // empty: no-op, must not panic

	worked := ks.ListTactics("nmap", 10)
	if len(worked) != 1 || worked[0].Outcome != knowledge.OutcomeWorked {
		t.Fatalf("nmap tactic not recorded as worked: %+v", worked)
	}
	if strings.Contains(worked[0].CommandPattern, "10.0.0.5") {
		t.Fatalf("target leaked into the stored pattern: %q", worked[0].CommandPattern)
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

// TestRunScopedCommandSurfacesGatewayKernelWarning preserves mixed-version
// compatibility: if an older gateway reports a partial-commit warning, the
// executor must still carry it into the operator-visible sandbox result.
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

func TestRuleDescription(t *testing.T) {
	short := ruleDescription("approved execution: ", "nmap -sV example.com")
	if short != "approved execution: nmap -sV example.com" {
		t.Fatalf("short command should pass through untouched, got %q", short)
	}
	if len(short) > maxRuleDescription {
		t.Fatalf("short description unexpectedly exceeds cap: %d", len(short))
	}

	longCommand := strings.Repeat("a", 600)
	got := ruleDescription("egress approved: ", longCommand)
	if len(got) > maxRuleDescription {
		t.Fatalf("ruleDescription must never exceed the gateway's %d-char cap, got %d: %q", maxRuleDescription, len(got), got)
	}
	if !strings.HasPrefix(got, "egress approved: ") {
		t.Fatalf("truncated description must keep its label prefix, got %q", got)
	}
	if !strings.HasSuffix(got, "...(truncated)") {
		t.Fatalf("truncated description must be marked as truncated, got %q", got)
	}

	// A real-world reproduction: this exact 485-char multi-step shell pipeline
	// (observed live against example.com) exceeds the cap once either label
	// is prefixed, and used to make the gateway reject AddTemporaryRule
	// outright — silently dropping the nftables rule, not just the audit text.
	realCommand := `cd ~/work/example.com && echo "example.com" > apex.txt; cat subs_clean.txt candidates_alterx.txt apex.txt | sort -u > candidates_all.txt; echo "=== Final candidate universe ==="; wc -l candidates_all.txt; echo "--- breakdown ---"; echo "Authoritative (passive CT+amass): $(wc -l < subs_clean.txt)"; echo "Alterx permutations:             $(wc -l < candidates_alterx.txt)"; echo "Apex example.com:                1"; echo "Total unique:                    $(wc -l < candidates_all.txt)"`
	if len("egress approved: "+realCommand) <= maxRuleDescription {
		t.Fatalf("fixture command must reproduce the bug once labeled, got %d chars", len("egress approved: "+realCommand))
	}
	for _, label := range []string{"approved execution: ", "egress approved: "} {
		if got := ruleDescription(label, realCommand); len(got) > maxRuleDescription {
			t.Fatalf("ruleDescription(%q, realCommand) = %d chars, want <= %d", label, len(got), maxRuleDescription)
		}
	}

	// Multi-byte runes must not be split mid-character.
	unicodeCommand := strings.Repeat("İ", 500)
	got = ruleDescription("egress approved: ", unicodeCommand)
	if !utf8.ValidString(got) {
		t.Fatalf("truncation produced invalid UTF-8: %q", got)
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

// TestProcessFunc_MCPRecord_NoTargetToolSkipsScopeCheck proves a tool with no
// target field (checksec_audit here, same shape as radare2_disasm and the
// new consolidate_hosts) dispatches successfully with NO gateway configured
// at all — before the NeedsTarget fix, this failed every real dispatch: it's
// pure local file analysis, nothing for the gateway to scope-check, but the
// old code required a target field regardless and blocked on its absence.
func TestProcessFunc_MCPRecord_NoTargetToolSkipsScopeCheck(t *testing.T) {
	fakeMCP := &fakeSandboxMCPClient{
		result: &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "checksec: NX enabled, PIE enabled"}}},
	}
	e := New(sandbox.NewBridge("unused"), nil, nil, fakeMCP)

	rec := execution.Record{
		ID: "rec-1", SessionID: "sess-1", Command: "checksec --file=/tmp/bin",
		MCPTool: "checksec_audit", MCPArgs: `{"file":"/tmp/bin"}`,
	}
	output, err := e.ProcessFunc()(context.Background(), rec)
	if err != nil {
		t.Fatalf("ProcessFunc: %v", err)
	}
	if !strings.Contains(output, "NX enabled") {
		t.Fatalf("output = %q, want it to contain the MCP result text", output)
	}
	if fakeMCP.calledTool != "checksec_audit" {
		t.Fatalf("sandboxMCP.CallTool called with tool = %q, want checksec_audit", fakeMCP.calledTool)
	}
}

// startTestSandboxContainer mirrors sandbox package's own test helper of the
// same name (unexported, so it can't be reused across packages) — a real
// standalone container from yummi/sandbox:local with the entrypoint
// overridden to a bare sleep (the real entrypoint.sh needs NET_ADMIN + a
// live gateway to do its routing setup, neither of which this test
// provides). Skips (not fails) when docker or the image is unavailable.
func startTestSandboxContainer(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	if err := exec.Command("docker", "image", "inspect", "yummi/sandbox:local").Run(); err != nil {
		t.Skip("yummi/sandbox:local image not available locally")
	}
	name := fmt.Sprintf("yummi-sandbox-executor-test-%d", time.Now().UnixNano())
	runArgs := []string{"run", "-d", "--rm", "--name", name, "--entrypoint", "/bin/sleep", "yummi/sandbox:local", "infinity"}
	if out, err := exec.Command("docker", runArgs...).CombinedOutput(); err != nil {
		t.Skipf("failed to start test sandbox container: %v (%s)", err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", name).Run()
	})
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if err := exec.Command("docker", "exec", name, "true").Run(); err == nil {
			return name
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Skip("test sandbox container did not become ready in time")
	return ""
}

// TestRunScopedCommandStreamForwardsChunks proves the streaming variant
// carries onChunk through the exact same scope-check-then-exec path
// RunScopedCommand uses — real gateway allow verdict, real docker exec —
// down to sandbox.Bridge.ExecStream, and that the concatenated chunks match
// the final result exactly.
func TestRunScopedCommandStreamForwardsChunks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	container := startTestSandboxContainer(t)

	socket := gateway.StartTestServer(t, false, func(req gateway.Request) gateway.Response {
		if req.Type == "check_scope" {
			return gateway.Response{Type: "scope_check_result", IP: req.IP, Verdict: "allow"}
		}
		return gateway.Response{Type: "error", Message: "unexpected request"}
	})
	pc := gateway.NewClient(socket)
	bridge := sandbox.NewBridge(container)

	var mu sync.Mutex
	var gotStdout, gotStderr strings.Builder
	onChunk := func(source string, chunk []byte) {
		mu.Lock()
		defer mu.Unlock()
		switch source {
		case "stdout":
			gotStdout.Write(chunk)
		case "stderr":
			gotStderr.Write(chunk)
		default:
			t.Errorf("unexpected chunk source %q", source)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// "true 10.0.0.1" gives scope.ExtractTargets a real target token (so this
	// exercises the gateway-checked branch, not the no-target bypass) while
	// being a real no-op command (true ignores its arguments, no output); the
	// for loop after it is the actual thing whose chunked output we assert on.
	result, err := RunScopedCommandStream(ctx, bridge, pc, "true 10.0.0.1 ; for i in 1 2; do echo chunk-$i; sleep 0.1; done", onChunk)
	if err != nil {
		t.Fatalf("RunScopedCommandStream: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotStdout.String() != result.Stdout {
		t.Fatalf("streamed stdout %q does not match Result.Stdout %q", gotStdout.String(), result.Stdout)
	}
	if gotStderr.String() != result.Stderr {
		t.Fatalf("streamed stderr %q does not match Result.Stderr %q", gotStderr.String(), result.Stderr)
	}
	if !strings.Contains(gotStdout.String(), "chunk-1") || !strings.Contains(gotStdout.String(), "chunk-2") {
		t.Fatalf("expected streamed stdout to contain both chunks, got %q", gotStdout.String())
	}
}

// TestProcessFuncThreadsOutputSinkIntoBridge is the highest-level proof of
// the whole streaming path: execution.Store's own OutputSink context value
// (installed by runWithHeartbeat) reaches all the way through
// executor.ProcessFunc → RunScopedCommandStream → sandbox.Bridge.ExecStream
// and back out as real "execution.output" events on the store's own
// pub/sub — the exact wiring cmd/orchestrator/main.go uses in production,
// exercised here without needing the whole orchestrator binary.
func TestProcessFuncThreadsOutputSinkIntoBridge(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	container := startTestSandboxContainer(t)

	socket := gateway.StartTestServer(t, false, func(req gateway.Request) gateway.Response {
		if req.Type == "check_scope" {
			return gateway.Response{Type: "scope_check_result", IP: req.IP, Verdict: "allow"}
		}
		return gateway.Response{Type: "error", Message: "unexpected request"}
	})
	pc := gateway.NewClient(socket)
	bridge := sandbox.NewBridge(container)
	e := New(bridge, pc, nil, nil)

	store := execution.NewStore()
	store.SetProcessor(e.ProcessFunc())

	subID, events, err := store.Subscribe(32)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer store.Unsubscribe(subID)

	rec, err := store.Enqueue(context.Background(), "s1", "p1", "for i in 1 2; do echo out-$i; sleep 0.1; done", "manual_terminal")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	var chunkCount int
	completed := false
	deadline := time.Now().Add(15 * time.Second)
	for !completed && time.Now().Before(deadline) {
		select {
		case evt := <-events:
			if evt.Record.ID != rec.ID {
				continue
			}
			if evt.Type == "execution.output" {
				chunkCount++
			}
			if evt.Type == "execution.completed" {
				completed = true
			}
		case <-time.After(20 * time.Millisecond):
		}
	}
	if !completed {
		t.Fatal("execution never completed")
	}
	if chunkCount == 0 {
		t.Fatal("expected at least one execution.output event to reach the store's pub/sub via ProcessFunc")
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
