package conductor

import "strings"

// Tier identifies a model-complexity tier supported by Yummi.
type Tier string

const (
	TierLite     Tier = "lite"
	TierStandard Tier = "standard"
	TierPro      Tier = "pro"
	TierExpert   Tier = "expert"
)

// TierConfig describes the runtime knobs for a tier.
type TierConfig struct {
	Tier        Tier
	Model       string
	MaxTokens   int
	NumCtx      int  // Ollama num_ctx context window tokens
	Thinking    bool // Qwen3 extended thinking mode
	Description string
}

// TierRegistry holds the tier → TierConfig mapping.
type TierRegistry struct {
	tiers map[Tier]TierConfig
}

// DefaultTierRegistry returns Yummi's default Qwen3 tier presets.
func DefaultTierRegistry() *TierRegistry {
	return &TierRegistry{
		tiers: map[Tier]TierConfig{
			TierLite: {
				Tier:        TierLite,
				Model:       "qwen3:4b",
				MaxTokens:   2048,
				NumCtx:      8192,
				Thinking:    false,
				Description: "4B parameters — fast, minimal RAM footprint.",
			},
			TierStandard: {
				Tier:        TierStandard,
				Model:       "qwen3:8b",
				MaxTokens:   4096,
				NumCtx:      16384,
				Thinking:    false,
				Description: "8B parameters — balanced quality and latency.",
			},
			TierPro: {
				Tier:        TierPro,
				Model:       "qwen3:14b",
				MaxTokens:   4096,
				NumCtx:      16384,
				Thinking:    false,
				Description: "14B parameters — recommended default for most hardware.",
			},
			TierExpert: {
				Tier:        TierExpert,
				Model:       "qwen3:32b",
				MaxTokens:   8192,
				NumCtx:      32768,
				Thinking:    true,
				Description: "32B parameters — deep reasoning, workstation-grade hardware required.",
			},
		},
	}
}

// Get returns the TierConfig for a tier name.
func (tr *TierRegistry) Get(t Tier) (TierConfig, bool) {
	if tr == nil {
		return TierConfig{}, false
	}
	cfg, ok := tr.tiers[t]
	return cfg, ok
}

// Resolve interprets its argument as a tier name or a literal Ollama model tag.
// Tier names return the matching registry entry; anything else is treated as a
// custom model tag wrapped under the Pro tier profile (16K context, no thinking).
// An empty input returns the Pro default.
func (tr *TierRegistry) Resolve(modelOrTier string) TierConfig {
	if tr == nil {
		return TierConfig{}
	}
	key := strings.ToLower(strings.TrimSpace(modelOrTier))
	if key == "" {
		return tr.tiers[TierPro]
	}
	if cfg, ok := tr.tiers[Tier(key)]; ok {
		return cfg
	}
	// Custom model tag — wrap under Pro profile.
	base := tr.tiers[TierPro]
	base.Model = strings.TrimSpace(modelOrTier)
	base.Description = "custom model tag — Pro profile settings"
	return base
}

// TierNames returns the known tier identifiers.
func (tr *TierRegistry) TierNames() []Tier {
	if tr == nil {
		return nil
	}
	out := make([]Tier, 0, len(tr.tiers))
	for k := range tr.tiers {
		out = append(out, k)
	}
	return out
}
