package report

import (
	"strings"
	"testing"
	"time"

	"github.com/n0connect/yummi/orchestrator/internal/knowledge"
)

func sampleData() ReportData {
	return ReportData{
		SessionID:   "sess_test_123",
		GeneratedAt: time.Date(2026, 9, 10, 15, 0, 0, 0, time.UTC),
		Targets: []knowledge.Target{
			{Value: "example.com", InScope: true},
		},
		Assets: []knowledge.AssetNode{
			{Type: knowledge.AssetDomain, Value: "example.com", CreatedAt: 1725980000},
			{Type: knowledge.AssetSubdomain, Value: "api.example.com", CreatedAt: 1725980100},
		},
		Findings: []knowledge.Finding{
			{
				Title:       "SQL Injection in Login Endpoint",
				Severity:    "critical",
				Description: "Vulnerable to boolean-blind SQL injection via 'username' parameter.",
				Evidence:    "sqlmap identified parameter 'username' as injectable",
				Source:      "agent",
			},
			{
				Title:       "Reflected XSS on search page",
				Severity:    "high",
				Description: "Payload <script>alert(1)</script> reflected unescaped.",
				Evidence:    "GET /search?q=%3Cscript%3Ealert(1)%3C/script%3E -> 200 OK with unescaped script tag",
				Source:      "agent",
			},
		},
	}
}

func TestGenerate_HackerOne(t *testing.T) {
	d := sampleData()
	out, ct, err := Generate(d, FormatHackerOne)
	if err != nil {
		t.Fatalf("Generate(HackerOne): %v", err)
	}
	if ct != "text/markdown" {
		t.Errorf("content-type = %q, want 'text/markdown'", ct)
	}
	if !strings.Contains(out, "CWE-89: SQL Injection") {
		t.Errorf("expected SQLi CWE in HackerOne report, got:\n%s", out)
	}
	if !strings.Contains(out, "CVSS v3.1: 9.8") {
		t.Errorf("expected 9.8 CVSS for critical finding, got:\n%s", out)
	}
}

func TestGenerate_Bugcrowd(t *testing.T) {
	d := sampleData()
	out, ct, err := Generate(d, FormatBugcrowd)
	if err != nil {
		t.Fatalf("Generate(Bugcrowd): %v", err)
	}
	if ct != "text/markdown" {
		t.Errorf("content-type = %q, want 'text/markdown'", ct)
	}
	if !strings.Contains(out, "**Priority:** P1 (CRITICAL)") {
		t.Errorf("expected P1 for critical finding, got:\n%s", out)
	}
}

func TestGenerate_Executive(t *testing.T) {
	d := sampleData()
	out, ct, err := Generate(d, FormatExecutive)
	if err != nil {
		t.Fatalf("Generate(Executive): %v", err)
	}
	if ct != "text/markdown" {
		t.Errorf("content-type = %q, want 'text/markdown'", ct)
	}
	if !strings.Contains(out, "Discovered Asset Inventory") {
		t.Errorf("expected asset inventory in executive report, got:\n%s", out)
	}
}

func TestGenerate_JSON(t *testing.T) {
	d := sampleData()
	out, ct, err := Generate(d, FormatJSON)
	if err != nil {
		t.Fatalf("Generate(JSON): %v", err)
	}
	if ct != "application/json" {
		t.Errorf("content-type = %q, want 'application/json'", ct)
	}
	if !strings.Contains(out, `"session_id": "sess_test_123"`) {
		t.Errorf("expected session_id in JSON output, got:\n%s", out)
	}
}
