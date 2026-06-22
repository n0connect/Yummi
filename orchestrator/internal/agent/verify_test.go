package agent

import "testing"

func TestVerify_NonZeroExitIsFailure(t *testing.T) {
	o, reason := Verify(ToolResult{Content: "boom", IsError: true})
	if o != OutcomeFailure || reason == "" {
		t.Fatalf("got %v reason=%q", o, reason)
	}
}

func TestVerify_ZeroExitWithFailurePattern_DowngradedToFailure(t *testing.T) {
	cases := []string{
		"bash: nmapp: command not found",
		"Note: Host seems down. 0 hosts up",
	}
	for _, c := range cases {
		if o, _ := Verify(ToolResult{Content: c, IsError: false}); o != OutcomeFailure {
			t.Errorf("expected failure for %q", c)
		}
	}
}

func TestVerify_CleanOutputIsSuccess(t *testing.T) {
	if o, _ := Verify(ToolResult{Content: "PORT 80/tcp open http", IsError: false}); o != OutcomeSuccess {
		t.Fatal("expected success")
	}
}

func TestVerifyDetectsGatewayBlocked(t *testing.T) {
	result := ToolResult{
		IsError: true,
		Content: `GATEWAY_BLOCKED: target "8.8.8.8" is not in scope`,
	}
	outcome, reason := Verify(result)
	if outcome != OutcomeBlocked {
		t.Fatalf("expected OutcomeBlocked, got %v", outcome)
	}
	if reason == "" {
		t.Fatal("expected a non-empty reason")
	}
}

func TestVerifyStillClassifiesGenericFailures(t *testing.T) {
	result := ToolResult{IsError: true, Content: "command not found: nmpa"}
	outcome, _ := Verify(result)
	if outcome != OutcomeFailure {
		t.Fatalf("expected OutcomeFailure for a non-gateway error, got %v", outcome)
	}
}
