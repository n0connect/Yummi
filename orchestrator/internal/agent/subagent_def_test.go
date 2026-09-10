package agent

import (
	"path/filepath"
	"testing"
)

func TestLoadSubAgentDefs_AllBuiltinSubAgents(t *testing.T) {
	// From orchestrator module root, ../persona/v0.1/subagents
	dir := filepath.Join("..", "..", "..", "persona", "v0.1", "subagents")
	defs, err := LoadSubAgentDefs(dir)
	if err != nil {
		t.Fatalf("LoadSubAgentDefs(%q): %v", dir, err)
	}

	if len(defs) == 0 {
		t.Fatalf("expected subagent definitions to load, got 0 from %s", dir)
	}

	expectedRoles := map[string]bool{
		"exploiter":       false,
		"osint":           false,
		"recon":           false,
		"reverser":        false,
		"source-analysis": false,
		"web":             false,
		"api-hunter":      false,
		"js-miner":        false,
		"auth-auditor":    false,
		"cloud-recon":     false,
	}

	for _, d := range defs {
		if d.Role == "" {
			t.Errorf("subagent has empty role: %+v", d)
		}
		if d.Identity == "" {
			t.Errorf("subagent %q has empty identity", d.Role)
		}
		if _, ok := expectedRoles[d.Role]; ok {
			expectedRoles[d.Role] = true
		}
	}

	for role, found := range expectedRoles {
		if !found {
			t.Errorf("expected subagent role %q was not loaded", role)
		}
	}
}
