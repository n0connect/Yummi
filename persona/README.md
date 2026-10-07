# persona — AI Assistant Behavior

> Quick local overview. For the full, kept-current reference (the two independent personas, safety boundaries explained in depth, golden tests, why `tier_capabilities/` isn't loaded at runtime), see [docs/persona/persona-and-skills.md](../docs/persona/persona-and-skills.md) and [docs/agent/skills-and-subagents.md](../docs/agent/skills-and-subagents.md).

YAML and JSON configuration files define conductor behavior, the independent agent persona, skills, and sub-agent roles. The conductor, agent, skill registry, and sub-agent loader each read the files they own at startup; no single loader consumes this whole tree.

## Layout

```
v0.1/
  core_identity.yaml       Who the assistant is — name, role, operating constraints
  methodology.yaml         How it approaches pentest tasks — recon -> exploit -> report
  safety_boundaries.yaml   Hard stops — what the assistant will never do
  output_schema.json       JSON schema for conductor ChatResponse validation
  agent.yaml               Agent-task identity and graph-loop instructions
  skills/                  Declarative playbooks loaded by the skill registry
  subagents/               Specialist roles available to HANDOFF
  tier_capabilities/       Reference docs for each tier's profile (NOT loaded by Go — see note below)
    standard.yaml / expert.yaml
  golden_tests/            Regression fixtures — input -> expected intent
    *.yaml
```

## Model tiers

Tiers are selected via `YUMMI_OLLAMA_TIER`. The actual model/context/thinking-mode values are hardcoded in `orchestrator/internal/conductor/tier.go`'s `DefaultTierRegistry()` — **not** loaded from `tier_capabilities/*.yaml`:

| Tier | Model | Context window |
|---|---|---|
| `standard` (registry + install default) | gpt-oss:20b | 32768 |
| `expert` | gpt-oss:120b | 32768 |

Only two tiers exist because gpt-oss only ships in two real sizes on Ollama — a third or fourth name would just alias one of these two models under a different label.

**`standard`/20B is a floor for autonomous work, not a recommendation** — live-tested (2026-10-07) against a real CVE lab, it did not reliably track the target or use the agent's tool-calling protocol across a multi-step task. `expert`, a larger local model (70B+), or an OpenRouter override is the realistic choice for sustained autonomy; see `tier.go`'s `DefaultTierRegistry` doc comment for the full writeup.

Setting `YUMMI_OLLAMA_MODEL` overrides the tier model. Without an explicit model, `YUMMI_OLLAMA_TIER` selects the matching preset.

**`tier_capabilities/*.yaml` is currently reference documentation only** — each file describes that tier's intended strengths/limitations for a human reader, but no Go code reads this directory (verified: `grep -rn tier_capabilities orchestrator --include='*.go'` returns nothing). If you change a tier's actual behavior, edit `tier.go`'s `DefaultTierRegistry()` map directly; update the matching `tier_capabilities/<name>.yaml` afterward to keep the description honest, but know that editing the YAML alone changes nothing at runtime.

## Adding a new tier

1. Add an entry to `DefaultTierRegistry()`'s map in `orchestrator/internal/conductor/tier.go`.
2. Add/update the matching `tier_capabilities/<name>.yaml` for human reference (optional, not load-bearing).
3. Test: `cd orchestrator && go test ./internal/conductor -run TestTier`

## Teaching the agent a new tool

There is no executable allowlist — the validator gates on risk, not tool identity. Skill YAML does contain informational `tools` metadata used for selection and prompt context, so it must stay aligned with the sandbox image. If a tool needs special handling:

- Install it in `sandbox/Dockerfile`; then add it to the relevant `skills/*.yaml` metadata and methodology.
- A tool family needs a structural risk floor: special-case it in `classifyRisk` (`orchestrator/internal/conductor/validator.go`).

## Updating safety boundaries

`safety_boundaries.yaml` defines absolute constraints — actions the assistant must never take regardless of user instruction. Changes here require careful review because they affect the trust model of the entire system.

## Golden tests

These fixtures are load-bearing regression tests. `internal/conductor.TestPersonaGoldenFixtures` reads every YAML file through `EvaluateGoldenFixtures`; the normal orchestrator `go test ./...` job therefore fails when deterministic boundary policy no longer produces the expected intent. Add a fixture whenever a high-confidence safety or missing-target failure is observed.

For example, a fixture-shaped intended behavior looks like:

```yaml
- input: "scan the target for open ports"
  expected_intent: "suggest_command"
  expected_tool: "nmap"
```

Run them directly with `cd orchestrator && go test ./internal/conductor -run TestPersonaGoldenFixtures`.
