package agent

import (
	"strings"
	"testing"
)

// TestAgentPersonaDocLoads verifies the shipped persona document parses and that
// every model-facing section is present. This guards the document-driven
// contract (no hard-coded prose) against an empty or malformed agent.yaml — it
// does not assert behavior, only that the persona is loadable and complete.
func TestAgentPersonaDocLoads(t *testing.T) {
	p, err := LoadAgentPersona("../../../persona/v0.1")
	if err != nil {
		t.Fatalf("load shipped persona: %v", err)
	}
	for name, section := range map[string]string{
		"identity":   p.Identity,
		"principles": p.Principles,
		"planning":   p.Planning,
	} {
		if strings.TrimSpace(section) == "" {
			t.Errorf("persona section %q is empty", name)
		}
	}
	// The persona must remain free of an injected tool catalogue (no copying).
	for _, banned := range []string{"nmap", "ffuf", "hydra", "gobuster", "-sV"} {
		if strings.Contains(strings.ToLower(p.Identity+p.Principles+p.Planning), banned) {
			t.Errorf("persona leaks tool/flag %q — must stay tool-agnostic", banned)
		}
	}
}
