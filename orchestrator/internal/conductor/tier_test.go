package conductor

import (
	"strings"
	"testing"
)

func TestTierRegistryDefaults(t *testing.T) {
	reg := DefaultTierRegistry()

	standard, ok := reg.Get(TierStandard)
	if !ok {
		t.Fatal("expected Standard tier to exist")
	}
	if !strings.Contains(strings.ToLower(standard.Model), "20b") {
		t.Errorf("expected Standard model to reference the 20B size, got %q", standard.Model)
	}

	expert, ok := reg.Get(TierExpert)
	if !ok {
		t.Fatal("expected Expert tier to exist")
	}
	if !strings.Contains(strings.ToLower(expert.Model), "120b") {
		t.Errorf("expected Expert model to reference the 120B size, got %q", expert.Model)
	}
	if expert.MaxTokens < 4096 {
		t.Errorf("expected Expert MaxTokens >= 4096, got %d", expert.MaxTokens)
	}
}

func TestTierResolveByName(t *testing.T) {
	reg := DefaultTierRegistry()

	got := reg.Resolve("expert")
	if got.Tier != TierExpert {
		t.Errorf("expected Resolve(\"expert\") to return Expert tier, got %s", got.Tier)
	}

	standardMixedCase := reg.Resolve("  Standard  ")
	if standardMixedCase.Tier != TierStandard {
		t.Errorf("expected Resolve(\"Standard\") to be case/whitespace tolerant, got %s", standardMixedCase.Tier)
	}
}

func TestTierResolveLiteralModel(t *testing.T) {
	reg := DefaultTierRegistry()

	got := reg.Resolve("mistral:7b")
	if got.Model != "mistral:7b" {
		t.Errorf("expected literal model to pass through, got %q", got.Model)
	}
	// Custom model should ride the Standard tier profile (recommended default).
	if got.Tier != TierStandard {
		t.Errorf("expected literal model to fall under Standard tier, got %s", got.Tier)
	}
}

func TestTierResolveEmpty(t *testing.T) {
	reg := DefaultTierRegistry()

	got := reg.Resolve("")
	if got.Tier != TierStandard {
		t.Errorf("expected empty input to default to Standard, got %s", got.Tier)
	}
}

func TestTierRegistryNilSafe(t *testing.T) {
	var reg *TierRegistry
	if _, ok := reg.Get(TierStandard); ok {
		t.Error("nil registry must not return tier")
	}
	got := reg.Resolve("expert")
	if got.Tier != "" {
		t.Errorf("nil Resolve must return zero value, got %s", got.Tier)
	}
}
