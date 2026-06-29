package gateway

import (
	"strings"
	"testing"
)

func TestClientGetStatus(t *testing.T) {
	socket := StartTestServer(t, false, func(req Request) Response {
		if req.Type != "get_status" {
			return Response{Type: "error", Message: "unexpected request type"}
		}
		return Response{
			Type:        "status",
			Level:       2,
			RulesActive: true,
			UptimeSecs:  77,
		}
	})

	c := NewClient(socket)
	status, err := c.GetStatus()
	if err != nil {
		t.Fatalf("get status: %v", err)
	}
	if status.Level != 2 || !status.RulesActive || status.UptimeSecs != 77 {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestClientCheckScope(t *testing.T) {
	socket := StartTestServer(t, false, func(req Request) Response {
		if req.Type != "check_scope" {
			return Response{Type: "error", Message: "unexpected request type"}
		}
		return Response{
			Type:    "scope_check_result",
			IP:      req.IP,
			InScope: req.IP == "10.0.0.5",
		}
	})

	c := NewClient(socket)
	ok, err := c.CheckScope("10.0.0.5")
	if err != nil {
		t.Fatalf("check scope: %v", err)
	}
	if !ok {
		t.Fatal("expected IP to be in scope")
	}
}

func TestClientPingChunkedResponse(t *testing.T) {
	socket := StartTestServer(t, true, func(req Request) Response {
		if req.Type != "ping" {
			return Response{Type: "error", Message: "unexpected request type"}
		}
		return Response{Type: "pong"}
	})

	c := NewClient(socket)
	if err := c.Ping(); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func TestClientReturnsGatewayError(t *testing.T) {
	socket := StartTestServer(t, false, func(req Request) Response {
		return Response{Type: "error", Message: "denied by policy"}
	})

	c := NewClient(socket)
	err := c.Ping()
	if err == nil {
		t.Fatal("expected gateway error")
	}
	if !strings.Contains(err.Error(), "denied by policy") {
		t.Fatalf("expected policy error, got: %v", err)
	}
}

func TestClientListRules(t *testing.T) {
	socket := StartTestServer(t, false, func(req Request) Response {
		if req.Type != "list_rules" {
			return Response{Type: "error", Message: "unexpected request type"}
		}
		expiresAt := int64(9999)
		return Response{
			Type: "rule_list",
			Rules: []RuleView{
				{ID: "manual-1", Action: "allow", Network: "10.0.0.5/32", Description: "[manual] test", ExpiresAt: &expiresAt, Source: "manual"},
			},
		}
	})

	c := NewClient(socket)
	rules, err := c.ListRules()
	if err != nil {
		t.Fatalf("list rules: %v", err)
	}
	if len(rules) != 1 || rules[0].Source != "manual" {
		t.Fatalf("unexpected rules: %+v", rules)
	}
}

func TestClientGetAuditLog(t *testing.T) {
	socket := StartTestServer(t, false, func(req Request) Response {
		if req.Type != "get_audit_log" || req.Limit != 10 {
			return Response{Type: "error", Message: "unexpected request"}
		}
		return Response{
			Type:          "audit_log",
			AuditRows:     []AuditRowView{{ID: 1, Timestamp: 1000, SrcIP: "gateway", DstIP: "10.0.0.5/32", Decision: "rule_added", Reason: "[manual] test"}},
			ChainVerified: true,
		}
	})

	c := NewClient(socket)
	rows, verified, err := c.GetAuditLog(10)
	if err != nil {
		t.Fatalf("get audit log: %v", err)
	}
	if !verified || len(rows) != 1 {
		t.Fatalf("unexpected audit log: rows=%+v verified=%v", rows, verified)
	}
}

func TestClientAddManualRule(t *testing.T) {
	socket := StartTestServer(t, false, func(req Request) Response {
		if req.Type != "add_manual_rule" || req.Reason == "" {
			return Response{Type: "error", Message: "unexpected request or missing reason"}
		}
		return Response{Type: "rule_added", RuleID: "manual-1", ExpiresAt: 1700}
	})

	c := NewClient(socket)
	ruleID, expiresAt, _, err := c.AddManualRule("10.0.0.5/32", 600, "emergency access for finding F-12")
	if err != nil {
		t.Fatalf("add manual rule: %v", err)
	}
	if ruleID != "manual-1" || expiresAt != 1700 {
		t.Fatalf("unexpected result: ruleID=%s expiresAt=%d", ruleID, expiresAt)
	}
}

// TestClientAddTemporaryRulePropagatesKernelWarning is a regression test: the
// gateway's policy engine can accept a rule while its OWN internal nftables
// kernel-level unlock silently fails — that used to be visible only in the
// gateway's logs. AddTemporaryRule must surface it to the Go caller (which
// the executor turns into an operator-visible execution warning) rather than
// reporting a bare, unqualified success.
func TestClientAddTemporaryRulePropagatesKernelWarning(t *testing.T) {
	const warning = "policy rule added but the nftables kernel unlock for 10.0.0.5/32 failed (no such device) — sandbox traffic to this target is still dropped"
	socket := StartTestServer(t, false, func(req Request) Response {
		if req.Type != "add_temporary_rule" {
			return Response{Type: "error", Message: "unexpected request"}
		}
		return Response{Type: "rule_added", RuleID: "temp-1", ExpiresAt: 1700, KernelWarning: warning}
	})

	c := NewClient(socket)
	ruleID, expiresAt, kernelWarning, err := c.AddTemporaryRule("10.0.0.5/32", 600, "agent execution")
	if err != nil {
		t.Fatalf("add temporary rule: %v", err)
	}
	if ruleID != "temp-1" || expiresAt != 1700 {
		t.Fatalf("unexpected result: ruleID=%s expiresAt=%d", ruleID, expiresAt)
	}
	if kernelWarning != warning {
		t.Fatalf("expected kernel warning to propagate, got %q", kernelWarning)
	}
}

// A clean rule add (no kernel failure) must leave kernelWarning empty.
func TestClientAddTemporaryRuleNoWarningOnSuccess(t *testing.T) {
	socket := StartTestServer(t, false, func(req Request) Response {
		return Response{Type: "rule_added", RuleID: "temp-2", ExpiresAt: 1800}
	})

	c := NewClient(socket)
	_, _, kernelWarning, err := c.AddTemporaryRule("10.0.0.6/32", 600, "agent execution")
	if err != nil {
		t.Fatalf("add temporary rule: %v", err)
	}
	if kernelWarning != "" {
		t.Fatalf("expected no kernel warning, got %q", kernelWarning)
	}
}

func TestClientGetFingerprintConfig(t *testing.T) {
	ttl := uint8(64)
	socket := StartTestServer(t, false, func(req Request) Response {
		if req.Type != "get_fingerprint_config" {
			return Response{Type: "error", Message: "unexpected request type"}
		}
		return Response{
			Type:             "fingerprint_config",
			TTLNormalize:     &ttl,
			MACRandomization: true,
		}
	})

	c := NewClient(socket)
	cfg, err := c.GetFingerprintConfig()
	if err != nil {
		t.Fatalf("get fingerprint config: %v", err)
	}
	if cfg.TTLNormalize == nil || *cfg.TTLNormalize != 64 || !cfg.MACRandomization {
		t.Fatalf("unexpected fingerprint config: %+v", cfg)
	}
}

func TestClientGetEgressMode(t *testing.T) {
	socket := StartTestServer(t, false, func(req Request) Response {
		if req.Type != "get_egress_mode" {
			return Response{Type: "error", Message: "unexpected request type"}
		}
		return Response{Type: "egress_mode", Mode: "ask"}
	})

	c := NewClient(socket)
	mode, err := c.GetEgressMode()
	if err != nil {
		t.Fatalf("get egress mode: %v", err)
	}
	if mode != "ask" {
		t.Fatalf("expected mode 'ask', got %q", mode)
	}
}

func TestClientSetEgressMode(t *testing.T) {
	socket := StartTestServer(t, false, func(req Request) Response {
		if req.Type != "set_egress_mode" || req.Mode != "allow" {
			return Response{Type: "error", Message: "unexpected request"}
		}
		return Response{Type: "egress_mode", Mode: req.Mode}
	})

	c := NewClient(socket)
	mode, err := c.SetEgressMode("allow")
	if err != nil {
		t.Fatalf("set egress mode: %v", err)
	}
	if mode != "allow" {
		t.Fatalf("expected mode 'allow', got %q", mode)
	}
}

func TestClientSetEgressModeRejectsInvalid(t *testing.T) {
	c := NewClient("/nonexistent") // never reaches the socket — fails client-side
	_, err := c.SetEgressMode("turbo")
	if err == nil {
		t.Fatal("expected client-side rejection of invalid mode")
	}
	if !strings.Contains(err.Error(), "invalid egress mode") {
		t.Fatalf("expected invalid-mode error, got: %v", err)
	}
}

func TestClientCheckEgress(t *testing.T) {
	cases := map[string]string{
		"10.0.0.5": "allow",
		"8.8.8.8":  "block",
		"1.1.1.1":  "needs_approval",
	}
	socket := StartTestServer(t, false, func(req Request) Response {
		if req.Type != "check_scope" {
			return Response{Type: "error", Message: "unexpected request type"}
		}
		return Response{Type: "scope_check_result", IP: req.IP, Verdict: cases[req.IP]}
	})

	c := NewClient(socket)
	for ip, want := range cases {
		got, err := c.CheckEgress(ip)
		if err != nil {
			t.Fatalf("check egress %s: %v", ip, err)
		}
		if got != want {
			t.Fatalf("check egress %s: got %q, want %q", ip, got, want)
		}
	}
}

func TestClientCheckEgressFallsBackToInScope(t *testing.T) {
	// An older gateway that returns no verdict — CheckEgress must degrade to
	// in_scope→allow/block so it never silently proceeds.
	socket := StartTestServer(t, false, func(req Request) Response {
		return Response{Type: "scope_check_result", IP: req.IP, InScope: req.IP == "10.0.0.5"}
	})

	c := NewClient(socket)
	if v, _ := c.CheckEgress("10.0.0.5"); v != "allow" {
		t.Fatalf("expected fallback 'allow', got %q", v)
	}
	if v, _ := c.CheckEgress("8.8.8.8"); v != "block" {
		t.Fatalf("expected fallback 'block', got %q", v)
	}
}

func TestClientAddManualRuleRequiresReason(t *testing.T) {
	c := NewClient("/nonexistent") // never reaches the socket — fails client-side
	_, _, _, err := c.AddManualRule("10.0.0.5/32", 600, "")
	if err == nil {
		t.Fatal("expected error for empty reason")
	}
}
