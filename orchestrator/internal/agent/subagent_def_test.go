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
		"exploiter":                 false,
		"osint":                     false,
		"recon":                     false,
		"reverser":                  false,
		"source-analysis":           false,
		"web":                       false,
		"api-hunter":                false,
		"js-miner":                  false,
		"auth-auditor":              false,
		"cloud-recon":               false,
		"ad-operator":               false,
		"privesc-operator":          false,
		"pivot-operator":            false,
		"windows-operator":          false,
		"container-operator":        false,
		"cloud-operator":            false,
		"protocol-auditor":          false,
		"logic-hunter":              false,
		"token-specialist":          false,
		"ci-cd-auditor":             false,
		"evasion-specialist":        false,
		"cache-specialist":          false,
		"sso-saml-specialist":       false,
		"k8s-operator":              false,
		"modern-api-specialist":     false,
		"ai-redteam-operator":       false,
		"lfi-traversal-specialist":  false,
		"baas-cloud-auditor":        false,
		"kerberos-specialist":       false,
		"perimeter-operator":        false,
		"database-specialist":       false,
		"info-leak-hunter":          false,
		"mitm-operator":             false,
		"exploit-chain-specialist":  false,
		"poc-validator":             false,
		"detection-engineer":        false,
		"adcs-specialist":           false,
		"cloud-iam-pivoter":         false,
		"mobile-app-hunter":         false,
		"wireless-rf-operator":      false,
		"deser-gadget-hunter":       false,
		"supply-chain-auditor":      false,
		"cloud-privesc-specialist":  false,
		"iot-firmware-analyst":      false,
		"saas-identity-specialist":  false,
		"spa-hunter":                false,
		"rf-sdr-specialist":         false,
		"memory-analyst":            false,
		"email-security-specialist": false,
		"apt-emulation-operator":    false,
		"dfir-investigator":         false,
		"smart-spray-operator":      false,
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
