[Back to Project README](../../README.md) · [Docs index](../README.md)

# Persona Architecture

`persona/v0.1/` contains editable identity, methodology, safety, skill, role, and golden-boundary documents loaded at orchestrator startup. Runtime protocol instructions and prompt assembly live in Go; persona files provide behavioral configuration.

Operator-approved generated skills are persisted under `YUMMI_AUTO_SKILL_DIR` and merged into the live registry. For detailed skill and sub-agent documentation, see [Skills & Sub-Agents](../agent/skills-and-subagents.md).

---

## Table of Contents

- [Two Independent Personas](#two-personas)
- [The Conductor Persona (Chat & Proposals)](#conductor-persona)
- [The Agent Persona (Autonomous Task Loop)](#agent-persona)
- [Safety Boundaries & Target Requirements](#safety-boundaries)
- [Golden Persona Boundary Tests](#golden-tests)
- [Tier Capabilities Documentation](#tier-capabilities)
- [Degradation on Load Failures](#load-failure)

---

## Two Independent Personas <a id="two-personas"></a>

The system maintains two distinct persona loaders serving separate subsystems:

| Persona | Loaded By | Source Files | Purpose |
|---|---|---|---|
| **Conductor Persona** | `orchestrator/internal/conductor/persona.go` | `core_identity.yaml`, `safety_boundaries.yaml`, `methodology.yaml`, `output_schema.json` | Drives single-command chat intent parsing and proposals (`conductor.Engine`). |
| **Agent Persona** | `orchestrator/internal/agent/persona.go` | `agent.yaml` | Shapes disposition and reasoning within the multi-step task state machine (`agent.Runner`). |

---

## The Conductor Persona (Chat & Proposals) <a id="conductor-persona"></a>

- **`core_identity.yaml`**: Encodes operational values (`user_authority`, `privacy_first`, `minimal_footprint`, `transparency`, `ethical_only`) and conversational tone.
- **`methodology.yaml`**: Ordered methodology steps: clarify objective -> determine engagement phase -> pick minimal-footprint technique -> classify risk honestly -> present proposal with rationale -> interpret output and propose next step.
- Merged into a unified in-memory struct at startup; modifications require an orchestrator restart.

---

## The Agent Persona (Autonomous Task Loop) <a id="agent-persona"></a>

`agent.yaml` shapes disposition, not specific commands:
*"Deliberately model- and context-INDEPENDENT: it encodes a WAY OF REASONING — the disposition and guardrails of a disciplined expert operator — not tool names, not flags, not canned procedures, not answers. Strengthen the reasoning here; never script actions."*

It points the agent at the `yummi-pwnbox` Kali environment's on-box manifest (`/etc/yummi/pwnbox.md`, source `sandbox/pwnbox.md`), which holds the tool inventory, wordlist environment variables (set via `/etc/profile.d/yummi-pwnbox.sh` so `bash -lc` sees them), staged helpers, gateway behavior, and `/workspace`. The manifest is read on demand, not injected each turn, so the persona stays small and tool-agnostic. Detailed tool-specific techniques belong in declarative [Skills](../agent/skills-and-subagents.md#skills), keeping the core reasoning loop token-efficient and model-agnostic.

---

## Safety Boundaries & Target Requirements <a id="safety-boundaries"></a>

`safety_boundaries.yaml` defines mandatory operational guardrails:

1. **Mandatory Target Invariant**:
   - If a request asks to scan or probe a network without naming a specific host, IP, CIDR, or URL, the conductor must ask a clarifying question.
   - It will never guess a target or default to scanning local container subnets (`172.20.0.0/24`).
2. **Decisive Action Within Target Scope**:
   - Once a target is explicitly specified, the agent executes reconnaissance without arbitrary self-censorship, trusting lower-layer network controls (the Rust gateway) to enforce boundary policy.
3. **Transparent Scope Enforcement**:
   - The model is instructed that network scope is strictly and transparently enforced by the gateway daemon. If a connection is blocked, it records the observation without assuming personal permission failure.

---

## Golden Persona Boundary Tests <a id="golden-tests"></a>

`persona/v0.1/golden_tests/*.yaml` provide regression fixtures mapping user inputs to expected conductor intents (`suggest_command`, `start_task`, `ask_clarification`, `decline`).

Run golden boundary tests via:
```bash
cd orchestrator && go test ./internal/conductor -run TestPersonaGoldenFixtures
```

---

## Tier Capabilities Documentation <a id="tier-capabilities"></a>

`persona/v0.1/tier_capabilities/` (`standard.yaml`, `expert.yaml`) serve as informational documentation. Concrete model tags and context window configurations are hardcoded in `orchestrator/internal/conductor/tier.go` (`DefaultTierRegistry()`).

---

## Degradation on Load Failures <a id="load-failure"></a>

- **Conductor Failure**: If conductor persona files fail to parse, `conductor.NewEngine` logs a warning and initializes with an empty fallback persona, keeping chat operational.
- **Agent Failure**: If `agent.yaml` fails to load in `cmd/orchestrator/main.go`, production startup leaves the task loop disabled. This is startup wiring, not a claim that every low-level `Runner` constructor loads or validates that file.
