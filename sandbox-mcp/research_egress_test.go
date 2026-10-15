package main

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestResearchFailurePreservesOnlyTypedApproval(t *testing.T) {
	err := fmt.Errorf("wrapped HTTP error: %w", &researchEgressError{Kind: "egress_approval_required", Target: "1.1.1.1", IPs: []string{"1.1.1.1"}})
	var actual researchEgressError
	if json.Unmarshal([]byte(researchFailure(err, "unavailable")), &actual) != nil || actual.Target != "1.1.1.1" || len(actual.IPs) != 1 {
		t.Fatalf("approval lost across HTTP error: %+v", actual)
	}
	if got := researchFailure(fmt.Errorf("provider credential body must remain private"), "unavailable"); got != "unavailable" {
		t.Fatalf("ordinary error details escaped: %s", got)
	}
}
