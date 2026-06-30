# persona — AI Assistant Behavior

YAML and JSON configuration files that define the AI assistant's identity, methodology, safety boundaries, and tool knowledge. The conductor engine (`orchestrator/internal/conductor/`) loads these at startup.

## Layout

```
v0.1/
  core_identity.yaml       Who the assistant is — name, role, operating constraints
  methodology.yaml         How it approaches pentest tasks — recon → exploit → report
  safety_boundaries.yaml   Hard stops — what the assistant will never do
  output_schema.json       JSON schema for conductor ChatResponse validation
  tier_capabilities/       Reference docs for each tier's profile (NOT loaded by Go — see note below)
    lite.yaml / standard.yaml / pro.yaml / expert.yaml
  golden_tests/            Regression fixtures — input → expected intent
    *.yaml
```

## Model tiers

Tiers are selected via `YUMMI_OLLAMA_TIER`. The actual model/context/thinking-mode values are hardcoded in `orchestrator/internal/conductor/tier.go`'s `DefaultTierRegistry()` — **not** loaded from `tier_capabilities/*.yaml`:

| Tier | Model | Context window |
|---|---|---|
| `lite` | qwen3:4b | 8192 |
| `standard` (default) | qwen3:8b | 16384 |
| `pro` | qwen3:14b | 16384 |
| `expert` | qwen3:32b | 32768 (extended thinking mode) |

Setting `YUMMI_OLLAMA_MODEL` overrides the tier model. Setting `YUMMI_OLLAMA_TIER` overrides `YUMMI_OLLAMA_MODEL`.

**`tier_capabilities/*.yaml` is currently reference documentation only** — each file describes that tier's intended strengths/limitations for a human reader, but no Go code reads this directory (verified: `grep -rn tier_capabilities orchestrator --include='*.go'` returns nothing). If you change a tier's actual behavior, edit `tier.go`'s `DefaultTierRegistry()` map directly; update the matching `tier_capabilities/<name>.yaml` afterward to keep the description honest, but know that editing the YAML alone changes nothing at runtime.

## Adding a new tier

1. Add an entry to `DefaultTierRegistry()`'s map in `orchestrator/internal/conductor/tier.go`.
2. Add/update the matching `tier_capabilities/<name>.yaml` for human reference (optional, not load-bearing).
3. Test: `cd orchestrator && go test ./internal/conductor -run TestTier`

## Teaching the agent a new tool

There is no tool registry file and no tool allowlist — the validator gates on risk, not on tool identity. The model relies on its own pretrained knowledge of pentest tools; `persona/v0.1/agent.yaml`'s `identity` is explicit about this ("you are given no catalogue and need none"). If a tool needs special handling:

- Niche/unusual tool the model doesn't already know well: add a short usage note to `core_identity.yaml` or `agent.yaml`'s prose (sparingly — these are reasoning documents, not a reference manual).
- A tool family needs a structural risk floor: special-case it in `classifyRisk` (`orchestrator/internal/conductor/validator.go`).

## Updating safety boundaries

`safety_boundaries.yaml` defines absolute constraints — actions the assistant must never take regardless of user instruction. Changes here require careful review because they affect the trust model of the entire system.

## Golden tests

**Not currently wired to an automated runner** — there is no Go code or script that reads `golden_tests/*.yaml` and asserts against it; verified no consumer exists anywhere in the repo. These are fixture-shaped documents describing intended (input, intent) pairs for a regression suite that hasn't been built yet. Treat them as a spec for that future runner, not as something CI already checks.

`golden_tests/*.yaml` contain expected (input, intent) pairs for regression testing:

```yaml
- input: "scan the target for open ports"
  expected_intent: "suggest_command"
  expected_tool: "nmap"
```

Run: `cd orchestrator && go test ./internal/conductor -run TestGolden`
