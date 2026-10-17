//go:build linux

package executor_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/n0connect/yummi/orchestrator/internal/api"
	"github.com/n0connect/yummi/orchestrator/internal/execution"
	"github.com/n0connect/yummi/orchestrator/internal/executor"
	"github.com/n0connect/yummi/orchestrator/internal/gateway"
)

// This adapter only translates method signatures to the real SDK session.
// The transport starts the installed sandbox server as UID1000; no provider,
// resolver, HTTP transport or gateway response is replaced.
type researchMCPSession struct{ session *mcp.ClientSession }

func (c researchMCPSession) CallTool(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
	return c.session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
}

func researchWait(t *testing.T, store *execution.Store, id string, status string) execution.Record {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		rec, ok := store.Get(id)
		if ok && rec.Status == status {
			return rec
		}
		if ok && (rec.Status == execution.StatusFailed || rec.Status == execution.StatusCompleted || rec.Status == execution.StatusAwaitingEgressApproval) {
			t.Fatalf("expected %s, got %+v", status, rec)
		}
		time.Sleep(10 * time.Millisecond)
	}
	rec, _ := store.Get(id)
	t.Fatalf("timed out waiting for %s: %+v", status, rec)
	return rec
}

func TestResearchApprovalRealSandboxIntegration(t *testing.T) {
	if os.Getenv("YUMMI_RESEARCH_INTEGRATION") != "1" {
		t.Skip("run only through qa/Dockerfile.research-integration")
	}
	if os.Geteuid() != 0 {
		t.Fatal("operator executor must run as root; MCP child runs as UID1000")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		t.Fatal("disposable Docker runtime required")
	}
	pc := gateway.NewClient("/run/yummi/research-gateway.sock")
	if err := pc.Ping(); err != nil {
		t.Fatal(err)
	}
	var policy *http.Server
	startPolicy := func(t *testing.T, client *gateway.Client) {
		ln, err := net.Listen("tcp", "172.20.0.3:9081")
		if err != nil {
			t.Fatal(err)
		}
		policy = &http.Server{Handler: api.NewSandboxPolicyRouter(client), ReadHeaderTimeout: time.Second}
		go func() { _ = policy.Serve(ln) }()
	}
	startPolicy(t, pc)
	defer func() {
		if policy != nil {
			_ = policy.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.Command("/usr/local/bin/yummi-sandbox-mcp")
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/home/pentester", "YUMMI_WEB_SEARCH_ENABLED=true", "YUMMI_BROWSER_HEADLESS=true"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 1000, Gid: 1000, Groups: []uint32{}}}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "real-research-integration", Version: "1"}, nil).Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", cmd.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]string{}
	for _, line := range strings.Split(string(status), "\n") {
		if key, value, ok := strings.Cut(line, ":"); ok {
			fields[key] = strings.Join(strings.Fields(value), " ")
		}
	}
	for _, key := range []string{"Uid", "Gid"} {
		if fields[key] != "1000 1000 1000 1000" {
			t.Fatalf("sandbox %s: %s", key, fields[key])
		}
	}
	for _, key := range []string{"CapEff", "CapPrm", "CapAmb"} {
		if fields[key] != "0000000000000000" {
			t.Fatalf("sandbox retained %s: %s", key, fields[key])
		}
	}
	if fields["Groups"] != "" {
		t.Fatalf("sandbox inherited supplementary groups: %s", fields["Groups"])
	}
	store := execution.NewStore()
	store.SetProcessor(executor.New(nil, pc, nil, researchMCPSession{session}).ProcessFunc())
	enqueue := func(t *testing.T, url string) execution.Record {
		t.Helper()
		rec, err := store.EnqueueMCP(ctx, "research-integration", "", "web_fetch", map[string]any{"url": url}, "web_fetch "+url, "agent")
		if err != nil {
			t.Fatal(err)
		}
		return rec
	}
	setMode := func(t *testing.T, mode string) {
		t.Helper()
		if _, err := pc.SetEgressMode(mode); err != nil {
			t.Fatal(err)
		}
	}
	clearTarget := func(t *testing.T) {
		t.Helper()
		rules, err := pc.ListRules()
		if err != nil {
			t.Fatal(err)
		}
		for _, rule := range rules {
			if rule.Network == "1.1.1.1/32" {
				if _, err := pc.RemoveRule(rule.ID); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	assertHeld := func(t *testing.T, rec execution.Record) execution.Record {
		t.Helper()
		held := researchWait(t, store, rec.ID, execution.StatusAwaitingEgressApproval)
		var ips []string
		if err := json.Unmarshal([]byte(held.PinnedIPs), &ips); err != nil {
			t.Fatal(err)
		}
		if held.EgressTarget != "1.1.1.1" || len(ips) != 1 || ips[0] != "1.1.1.1" {
			t.Fatalf("wrong sandbox-approved target: %+v", held)
		}
		return held
	}
	t.Run("literal_IP_operator_approval_requeues_without_second_hold", func(t *testing.T) {
		setMode(t, "ask")
		held := assertHeld(t, enqueue(t, "https://1.1.1.1/"))
		_, err := store.ResolveEgress(held.ID, true, func() error { return executor.UnlockEgressTarget(pc, held.EgressTarget, held.Command, "1.1.1.1") })
		if err != nil {
			t.Fatal(err)
		}
		if verdict, err := pc.CheckEgress("1.1.1.1"); err != nil || verdict != "allow" {
			t.Fatalf("approval not installed: %s %v", verdict, err)
		}
		rules, err := pc.ListRules()
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, rule := range rules {
			if rule.Network == "1.1.1.1/32" {
				found = true
			}
		}
		if !found {
			t.Fatal("actual gateway rule missing")
		}
		final := researchWait(t, store, held.ID, execution.StatusFailed)
		if !strings.Contains(final.Output, "Source fetch blocked or unavailable") {
			t.Fatalf("expected real network-none fetch failure after retry: %+v", final)
		}
		clearTarget(t)
	})
	t.Run("hostname_DoH_approval_retries_without_host_resolution", func(t *testing.T) {
		setMode(t, "ask")
		held := assertHeld(t, enqueue(t, "https://example.com/"))
		_, err := store.ResolveEgress(held.ID, true, func() error {
			return executor.UnlockEgressTarget(pc, held.EgressTarget, held.Command, "1.1.1.1")
		})
		if err != nil {
			t.Fatal(err)
		}
		if verdict, err := pc.CheckEgress("1.1.1.1"); err != nil || verdict != "allow" {
			t.Fatalf("DoH approval missing: %s %v", verdict, err)
		}
		final := researchWait(t, store, held.ID, execution.StatusFailed)
		if !strings.Contains(final.Output, "Source fetch blocked or unavailable") {
			t.Fatalf("expected real network-none DoH failure after retry: %+v", final)
		}
		clearTarget(t)
	})
	t.Run("hostname_holds_DoH_endpoint_before_DNS", func(t *testing.T) {
		setMode(t, "ask")
		held := assertHeld(t, enqueue(t, "https://example.com/"))
		if _, err := store.ResolveEgress(held.ID, false, nil); err != nil {
			t.Fatal(err)
		}
		final := researchWait(t, store, held.ID, execution.StatusFailed)
		if !strings.Contains(final.Error, "operator denied") {
			t.Fatalf("denial missing: %+v", final)
		}
		if verdict, err := pc.CheckEgress("1.1.1.1"); err != nil || verdict != "needs_approval" {
			t.Fatalf("denial opened gateway: %s %v", verdict, err)
		}
	})
	t.Run("block_all_fails_without_approval", func(t *testing.T) {
		setMode(t, "block_all")
		final := researchWait(t, store, enqueue(t, "https://1.1.1.1/").ID, execution.StatusFailed)
		if final.EgressTarget != "" || final.PinnedIPs != "" {
			t.Fatalf("blocked request generated approval: %+v", final)
		}
	})
	t.Run("malformed_request_and_operator_mutators_refused", func(t *testing.T) {
		for _, body := range []string{`{"ip":"example.com"}`, `{"ip":"1.1.1.1","mode":"allow_all"}`, `{"ip":"1.1.1.1"} {}`} {
			resp, err := http.Post("http://172.20.0.3:9081/v1/egress/check", "application/json", strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != 400 {
				t.Fatalf("malformed policy request got %d", resp.StatusCode)
			}
		}
		for _, path := range []string{"/api/v1/gateway/egress-mode", "/api/v1/gateway/rules", "/api/v1/executions/anything/resolve-egress", "/v1/egress/approve"} {
			req, _ := http.NewRequest(http.MethodPost, "http://172.20.0.3:9081"+path, bytes.NewBufferString(`{"mode":"allow_all"}`))
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != 404 && resp.StatusCode != 405 {
				t.Fatalf("mutator %s accessible: %d", path, resp.StatusCode)
			}
		}
		mode, err := pc.GetEgressMode()
		if err != nil || mode != "block_all" {
			t.Fatalf("policy changed: %s %v", mode, err)
		}
		final := researchWait(t, store, enqueue(t, "https://127.0.0.1/").ID, execution.StatusFailed)
		if final.EgressTarget != "" {
			t.Fatalf("malformed research request generated approval: %+v", final)
		}
	})
	t.Run("unavailable_gateway_and_policy_fail_closed", func(t *testing.T) {
		setMode(t, "ask")
		_ = policy.Close()
		http.DefaultClient.CloseIdleConnections()
		startPolicy(t, gateway.NewClient("/run/yummi/absent.sock"))
		resp, err := http.Post("http://172.20.0.3:9081/v1/egress/check", "application/json", strings.NewReader(`{"ip":"1.1.1.1"}`))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != 503 {
			t.Fatalf("missing gateway got %d", resp.StatusCode)
		}
		final := researchWait(t, store, enqueue(t, "https://1.1.1.1/").ID, execution.StatusFailed)
		if final.EgressTarget != "" {
			t.Fatalf("unavailable gateway generated approval: %+v", final)
		}
		_ = policy.Close()
		policy = nil
		final = researchWait(t, store, enqueue(t, "https://1.1.1.1/").ID, execution.StatusFailed)
		if final.EgressTarget != "" {
			t.Fatalf("unavailable policy generated approval: %+v", final)
		}
	})
}
