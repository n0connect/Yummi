package conductor

import (
	"os"
	"strings"
	"testing"
)

func TestValidatorInScope(t *testing.T) {
	v := NewValidatorFromEnv()

	if !v.InScope("192.168.1.1") {
		t.Error("Should allow target when no scope restrictions")
	}
	if !v.InScope("example.com") {
		t.Error("Should allow target when no scope restrictions")
	}
}

func TestValidatorInScopeWithEnv(t *testing.T) {
	os.Setenv("PENTEST_SCOPE_ALLOW", "192.168.1.0/24,example.com")
	os.Setenv("PENTEST_SCOPE_BLOCK", "192.168.1.100")
	defer func() {
		os.Unsetenv("PENTEST_SCOPE_ALLOW")
		os.Unsetenv("PENTEST_SCOPE_BLOCK")
	}()

	v := NewValidatorFromEnv()

	if v.InScope("192.168.1.100") {
		t.Error("Should block explicitly blocked IP")
	}

	if !v.InScope("192.168.1.50") {
		t.Error("Should allow IP in allowed subnet")
	}

	if !v.InScope("example.com") {
		t.Error("Should allow explicitly allowed domain")
	}

	if v.InScope("10.0.0.1") {
		t.Error("Should block IP outside allowed scope")
	}
}

// TestValidatorRiskPassthrough verifies that the model's declared risk_level
// is preserved by the validator (it is the primary source of truth).
// The backend does NOT classify by flag names — it trusts the model's output
// and only elevates risk if the command contains logical chain operators (&&/||).
func TestValidatorRiskPassthrough(t *testing.T) {
	v := NewValidatorFromEnv()

	resp := v.ValidateChatResponse(ChatResponse{
		Proposals: []Proposal{
			{Command: "nmap -sV 192.168.1.1", RiskLevel: "medium"},
			{Command: "sqlmap -u http://example.com --risk 3", RiskLevel: "high"},
			{Command: "ls -la", RiskLevel: "low"},
		},
	})

	if resp.Proposals[0].RiskLevel != "medium" {
		t.Errorf("model-declared medium risk should be preserved, got %s", resp.Proposals[0].RiskLevel)
	}
	if resp.Proposals[1].RiskLevel != "high" {
		t.Errorf("model-declared high risk should be preserved, got %s", resp.Proposals[1].RiskLevel)
	}
	if resp.Proposals[2].RiskLevel != "low" {
		t.Errorf("model-declared low risk should be preserved, got %s", resp.Proposals[2].RiskLevel)
	}
}

// TestValidatorApprovalRequired verifies that proposals with risk_level
// medium, high, or critical require approval, and low does not.
// Risk classification is model-supplied; the backend only enforces the threshold.
func TestValidatorApprovalRequired(t *testing.T) {
	v := NewValidatorFromEnv()

	resp := v.ValidateChatResponse(ChatResponse{
		Proposals: []Proposal{
			{Command: "subfinder -d example.com", RiskLevel: "low"},
			{Command: "nmap -sV -sC example.com", RiskLevel: "medium"},
		},
	})

	if resp.Proposals[0].RequiresApproval {
		t.Error("low risk should not require approval")
	}
	if !resp.Proposals[1].RequiresApproval {
		t.Error("medium risk should require approval")
	}
}

func TestValidatorLoopbackBlocking(t *testing.T) {
	v := NewValidatorFromEnv()

	if v.InScope("127.0.0.1") {
		t.Error("Should block loopback IP")
	}

	if v.InScope("::1") {
		t.Error("Should block IPv6 loopback")
	}
}

// TestValidatorBlocksOutOfScopeIP ensures targets outside the configured
// allow list are rejected even when the tool itself is permitted.
func TestValidatorBlocksOutOfScopeIP(t *testing.T) {
	t.Setenv("PENTEST_SCOPE_ALLOW", "10.0.0.0/24")
	t.Setenv("PENTEST_SCOPE_BLOCK", "")

	v := NewValidatorFromEnv()

	resp := v.ValidateChatResponse(ChatResponse{
		Intent: "suggest_command",
		Proposals: []Proposal{
			{ID: "p1", Command: "nmap -sV 8.8.8.8", TargetTool: "nmap",
				ToolArgs: map[string]any{"target": "8.8.8.8"}},
		},
	})

	if len(resp.Proposals) != 0 {
		t.Errorf("expected out-of-scope proposal to be dropped, got %d", len(resp.Proposals))
	}
	found := false
	for _, w := range resp.Warnings {
		if strings.Contains(w, "out of scope") || strings.Contains(w, "scope") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected scope warning, got %v", resp.Warnings)
	}
}

// TestValidatorAllowsInScopeIP is the positive counterpart: targets inside
// the allow list must survive validation.
func TestValidatorAllowsInScopeIP(t *testing.T) {
	t.Setenv("PENTEST_SCOPE_ALLOW", "10.0.0.0/24")
	t.Setenv("PENTEST_SCOPE_BLOCK", "")

	v := NewValidatorFromEnv()

	resp := v.ValidateChatResponse(ChatResponse{
		Intent: "suggest_command",
		Proposals: []Proposal{
			{ID: "p1", Command: "nmap -sV 10.0.0.42", TargetTool: "nmap",
				ToolArgs: map[string]any{"target": "10.0.0.42"}},
		},
	})

	if len(resp.Proposals) != 1 {
		t.Fatalf("expected in-scope proposal to survive, got %d (warnings=%v)",
			len(resp.Proposals), resp.Warnings)
	}
}

// TestValidatorEmptyCommandDropped ensures proposals with an empty/whitespace
// command are dropped with a warning.
func TestValidatorEmptyCommandDropped(t *testing.T) {
	v := NewValidatorFromEnv()

	resp := v.ValidateChatResponse(ChatResponse{
		Intent: "suggest_command",
		Proposals: []Proposal{
			{ID: "p1", Command: "   "},
			{ID: "p2", Command: ""},
		},
	})

	if len(resp.Proposals) != 0 {
		t.Errorf("expected empty-command proposals dropped, got %d", len(resp.Proposals))
	}
	if len(resp.Warnings) == 0 {
		t.Error("expected a warning about the empty command")
	}
}

func TestScopeSummary(t *testing.T) {
	v := NewValidator(ValidationConfig{Allow: []string{"192.168.1.0/24", "example.com"}})
	s := v.ScopeSummary()
	if !strings.Contains(s, "192.168.1.0/24") || !strings.Contains(s, "example.com") {
		t.Fatalf("scope summary missing entries: %q", s)
	}
	var nilV *Validator
	if nilV.ScopeSummary() != "" {
		t.Fatal("nil validator should return empty summary")
	}
}
