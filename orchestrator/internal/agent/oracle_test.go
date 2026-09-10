package agent

import (
	"testing"
)

func TestEvaluateActiveProof_XSS(t *testing.T) {
	input := ActiveProofInput{
		PoCType:       "xss",
		Evidence:      "<script>alert(1)</script>",
		ExploitOutput: "HTTP/1.1 200 OK\nContent-Type: text/html\n\n<div>Search: <script>alert(1)</script></div>",
	}
	res := EvaluateActiveProof(input)
	if !res.Verified || res.OracleKind != OracleDOMExecution {
		t.Fatalf("expected verified DOM execution, got: %+v", res)
	}
}

func TestEvaluateActiveProof_SSRF(t *testing.T) {
	input := ActiveProofInput{
		PoCType:       "ssrf",
		ExpectedToken: "CANARY_TOKEN_XYZ123",
		ExploitOutput: "Connection received from 169.254.169.254 with token CANARY_TOKEN_XYZ123",
	}
	res := EvaluateActiveProof(input)
	if !res.Verified || res.OracleKind != OracleOOBInteraction {
		t.Fatalf("expected verified OOB interaction, got: %+v", res)
	}
}

func TestEvaluateActiveProof_StatusDiff(t *testing.T) {
	input := ActiveProofInput{
		PoCType:        "status_diff",
		BaselineOutput: "HTTP/1.1 403 Forbidden\nAccess Denied",
		ExploitOutput:  "HTTP/1.1 200 OK\nWelcome Admin Dashboard",
	}
	res := EvaluateActiveProof(input)
	if !res.Verified || res.OracleKind != OracleActivePoC {
		t.Fatalf("expected verified ActivePoC for status diff, got: %+v", res)
	}
}

func TestEvaluateActiveProof_CORS(t *testing.T) {
	input := ActiveProofInput{
		PoCType:       "cors",
		ExploitOutput: "HTTP/1.1 200 OK\nAccess-Control-Allow-Origin: https://attacker.com\nAccess-Control-Allow-Credentials: true",
	}
	res := EvaluateActiveProof(input)
	if !res.Verified || res.OracleKind != OracleActivePoC {
		t.Fatalf("expected verified ActivePoC for CORS, got: %+v", res)
	}
}

func TestClassifyOracleKind(t *testing.T) {
	cases := []struct {
		title    string
		evidence string
		output   string
		want     OracleKind
	}{
		{"Stored XSS in profile", "<svg onload=alert(1)>", "", OracleDOMExecution},
		{"Blind SSRF via webhook", "token123", "", OracleOOBInteraction},
		{"Time-based SQLi on id param", "SLEEP(5)", "", OracleDifferential},
		{"CORS Misconfiguration", "ACAO: *", "", OracleActivePoC},
		{"Open port 22", "OpenSSH 8.9", "22/tcp open ssh", OracleToolEvidence},
	}
	for _, c := range cases {
		got := ClassifyOracleKind(c.title, c.evidence, c.output)
		if got != c.want {
			t.Errorf("ClassifyOracleKind(%q) = %q, want %q", c.title, got, c.want)
		}
	}
}
