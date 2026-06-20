# persona — AI Assistant Behavior

YAML and JSON configuration files that define the AI assistant's identity, methodology, safety boundaries, and tool knowledge. The conductor engine (`n1/internal/conductor/`) loads these at startup.

## Layout

```
v0.1/
  core_identity.yaml       Who the assistant is — name, role, operating constraints
  methodology.yaml         How it approaches pentest tasks — recon → exploit → report
  safety_boundaries.yaml   Hard stops — what the assistant will never do
  output_schema.json       JSON schema for conductor ChatResponse validation
  tool_knowledge.yaml      Tool registry — flags, use cases, example invocations
  tier_capabilities/       Per-tier capability overrides
    lite.yaml              llama3.2:3b — fast, limited tool set
    pro.yaml               llama3.1:8b — balanced
    expert.yaml            llama3.3:70b — full capability
    standard.yaml          Default when no tier is set
  golden_tests/            Regression fixtures — input → expected intent
    *.yaml
```

## Model tiers

Tiers are selected via `YUMMI_OLLAMA_TIER`:

| Tier | Model | Profile |
|---|---|---|
| `lite` | llama3.2:3b | Fast, ~4 GB VRAM, reduced tool set |
| `pro` | llama3.1:8b | Balanced, ~8 GB VRAM |
| `expert` | llama3.3:70b | Full capability, requires GPU |
| `standard` | qwen3:8b (default) | Dev default — good balance on Apple Silicon |

Setting `YUMMI_OLLAMA_MODEL` overrides the tier model. Setting `YUMMI_OLLAMA_TIER` overrides `YUMMI_OLLAMA_MODEL`.

## Adding a new tier

1. Copy an existing file from `tier_capabilities/` to `tier_capabilities/<name>.yaml`
2. Update `n1/internal/conductor/tier.go` — add to `tierPresets` map
3. Test: `cd n1 && go test ./internal/conductor -run TestTier`

## Adding tool knowledge

Edit `tool_knowledge.yaml`. Each entry provides the LLM with context about when and how to use a tool — this supplements the allowlist in `n1/internal/conductor/validator.go`.

```yaml
tools:
  - name: mytool
    category: recon          # recon | exploit | post-exploit | util
    description: "..."
    common_flags:
      - flag: "-p"
        description: "Port range"
    example_invocations:
      - "mytool -p 80,443 target.com"
```

## Updating safety boundaries

`safety_boundaries.yaml` defines absolute constraints — actions the assistant must never take regardless of user instruction. Changes here require careful review because they affect the trust model of the entire system.

## Golden tests

`golden_tests/*.yaml` contain expected (input, intent) pairs for regression testing:

```yaml
- input: "scan the target for open ports"
  expected_intent: "suggest_command"
  expected_tool: "nmap"
```

Run: `cd n1 && go test ./internal/conductor -run TestGolden`
