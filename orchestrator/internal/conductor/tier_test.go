package conductor

import (
	"strings"
	"testing"
)

func TestTierRegistryDefaults(t *testing.T) {
	reg := DefaultTierRegistry()

	lite, ok := reg.Get(TierLite)
	if !ok {
		t.Fatal("expected Lite tier to exist")
	}
	if !strings.Contains(strings.ToLower(lite.Model), "4b") {
		t.Errorf("expected Lite model to reference 4B size, got %q", lite.Model)
	}

	expert, ok := reg.Get(TierExpert)
	if !ok {
		t.Fatal("expected Expert tier to exist")
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

	proMixedCase := reg.Resolve("  Pro  ")
	if proMixedCase.Tier != TierPro {
		t.Errorf("expected Resolve(\"Pro\") to be case/whitespace tolerant, got %s", proMixedCase.Tier)
	}
}

func TestTierResolveLiteralModel(t *testing.T) {
	reg := DefaultTierRegistry()

	got := reg.Resolve("mistral:7b")
	if got.Model != "mistral:7b" {
		t.Errorf("expected literal model to pass through, got %q", got.Model)
	}
	// Custom model should ride the Pro tier profile (recommended default).
	if got.Tier != TierPro {
		t.Errorf("expected literal model to fall under Pro tier, got %s", got.Tier)
	}
}

func TestTierResolveEmpty(t *testing.T) {
	reg := DefaultTierRegistry()

	got := reg.Resolve("")
	if got.Tier != TierPro {
		t.Errorf("expected empty input to default to Pro, got %s", got.Tier)
	}
}

func TestTierRegistryNilSafe(t *testing.T) {
	var reg *TierRegistry
	if _, ok := reg.Get(TierLite); ok {
		t.Error("nil registry must not return tier")
	}
	got := reg.Resolve("expert")
	if got.Tier != "" {
		t.Errorf("nil Resolve must return zero value, got %s", got.Tier)
	}
}
