package conductor

import "strings"

// Tier identifies a model-complexity tier supported by Yummi.
type Tier string

const (
	TierStandard Tier = "standard"
	TierExpert   Tier = "expert"
)

// TierConfig describes the runtime knobs for a tier.
type TierConfig struct {
	Tier        Tier
	Model       string
	MaxTokens   int
	NumCtx      int  // Ollama num_ctx context window tokens
	Thinking    bool // model-dependent extended-reasoning toggle (Options.Think)
	Description string
}

// TierRegistry holds the tier → TierConfig mapping.
type TierRegistry struct {
	tiers map[Tier]TierConfig
}

// DefaultTierRegistry returns Yummi's default gpt-oss tier presets.
//
// Only two tiers exist because OpenAI's gpt-oss family only ships in two
// real sizes on Ollama — 20b and 120b — unlike the four-rung qwen3 ladder
// (4b/8b/14b/32b) this registry used before. A third or fourth named tier
// here would have to alias one of these two same models under a different
// name, which is worse than just not pretending a finer distinction exists.
//
// `standard` (20B) is NOT recommended for autonomous agent use — live-tested
// (2026-10-07, a gpt-oss-20b derivative, graph AND Eino backends, a real
// multi-stage CVE lab): it reliably lost track of the actual target IP
// across turns, invented nonexistent CLI syntax instead of using the
// agent's own tool-calling protocol, and on a SEPARATE run (a different
// 20B-class local tag) called finish with a fully fabricated exploit
// narrative — wrong target IP, a nonexistent tool, a fake flag — having
// made zero real tool calls at all. A real OpenRouter frontier model,
// tested the same week against the same lab, tracked the target correctly
// across 80+ turns and completed the full chain (RCE, a reverse-shell
// listener via the agent's own terminal-session tools, and privilege
// escalation) for real. 20B is kept ONLY as the lightweight default for
// trying the chat/UI flow without a large download; it is not a supported
// choice for `make start`-driven autonomous tasks. For real autonomous
// use, the floor is `expert` (gpt-oss:120b) or an equivalently large local
// model (120B+ parameters) — not a 70B-class or smaller tag, which is
// still well below the capability bar this floor is based on — or an
// OpenRouter/cloud override instead of staying local.
func DefaultTierRegistry() *TierRegistry {
	return &TierRegistry{
		tiers: map[Tier]TierConfig{
			TierStandard: {
				Tier:        TierStandard,
				Model:       "gpt-oss:20b",
				MaxTokens:   4096,
				NumCtx:      32768,
				Thinking:    false,
				Description: "20B parameters (MXFP4) — a lightweight quick-start default for trying the chat/UI flow, nothing more. NOT supported for autonomous multi-step agent tasks (live-tested: loses the target, invents tool syntax, needs server-side correction to function at all, and can fabricate an entire fake exploit run with zero real tool calls). For autonomous use, the floor is the expert tier (gpt-oss:120b) or an equivalently large local model (120B+) — not a 70B-class or smaller tag — or an OpenRouter cloud model.",
			},
			TierExpert: {
				Tier:        TierExpert,
				Model:       "gpt-oss:120b",
				MaxTokens:   8192,
				NumCtx:      32768,
				Thinking:    false,
				Description: "120B parameters (MXFP4) — workstation-grade hardware, a single 80GB GPU or 64GB+ Mac with offload. The recommended floor for autonomous multi-step work if staying fully local; a cloud override is the realistic alternative on smaller hardware.",
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
// custom model tag wrapped under the Standard tier profile (32K context, no
// thinking). An empty input returns the Standard default.
func (tr *TierRegistry) Resolve(modelOrTier string) TierConfig {
	if tr == nil {
		return TierConfig{}
	}
	key := strings.ToLower(strings.TrimSpace(modelOrTier))
	if key == "" {
		return tr.tiers[TierStandard]
	}
	if cfg, ok := tr.tiers[Tier(key)]; ok {
		return cfg
	}
	// Custom model tag — wrap under the Standard profile.
	base := tr.tiers[TierStandard]
	base.Model = strings.TrimSpace(modelOrTier)
	base.Description = "custom model tag — Standard profile settings"
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
