package conductor

import (
	"testing"
)

// TestValidatorRiskPassthrough verifies that the model's declared risk_level
// is preserved by the validator (it is the primary source of truth).
// The backend does NOT classify by flag names — it trusts the model's output
// and only elevates risk if the command contains logical chain operators (&&/||).
func TestValidatorRiskPassthrough(t *testing.T) {
	v := NewValidator()

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
	v := NewValidator()

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

// TestValidatorEmptyCommandDropped ensures proposals with an empty/whitespace
// command are dropped with a warning.
func TestValidatorEmptyCommandDropped(t *testing.T) {
	v := NewValidator()

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
