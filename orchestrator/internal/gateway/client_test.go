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
	ruleID, expiresAt, err := c.AddManualRule("10.0.0.5/32", 600, "emergency access for finding F-12")
	if err != nil {
		t.Fatalf("add manual rule: %v", err)
	}
	if ruleID != "manual-1" || expiresAt != 1700 {
		t.Fatalf("unexpected result: ruleID=%s expiresAt=%d", ruleID, expiresAt)
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

func TestClientAddManualRuleRequiresReason(t *testing.T) {
	c := NewClient("/nonexistent") // never reaches the socket — fails client-side
	_, _, err := c.AddManualRule("10.0.0.5/32", 600, "")
	if err == nil {
		t.Fatal("expected error for empty reason")
	}
}

