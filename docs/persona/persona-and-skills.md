← [Back to README](../../README.md) · [Docs index](../README.md)

# Persona

`persona/v0.1/` contains the editable identity, methodology, safety, skill, role, and golden-boundary documents loaded at orchestrator startup. Runtime protocol instructions and prompt assembly still live in Go; persona files are the behavioral configuration layer, not the entire prompt. Operator-approved generated skills are persisted separately under `YUMMI_AUTO_SKILL_DIR` and merged into the live registry. This page covers everything **except** skills and sub-agent definitions, which have their own deep-dive at [Skills & Sub-agents](../agent/skills-and-subagents.md).

## Table of contents

- [Two independent personas, not one](#two-personas)
- [The conductor persona — three files, one struct](#conductor-persona)
- [The agent persona — one file, model-independent by design](#agent-persona)
- [Safety boundaries — read this one in full](#safety-boundaries)
- [Golden tests](#golden-tests)
- [tier_capabilities/ — documentation only, NOT loaded at runtime](#tier-capabilities)
- [Load failures degrade the two paths differently](#load-failure)

---

## Two independent personas, not one <a id="two-personas"></a>

This is worth stating explicitly because it's easy to assume "the persona" is a single thing — it's actually **two separate loaders reading two separate, non-overlapping sets of files**, for two structurally different subsystems:

| Persona | Loaded by | Files | Serves |
|---|---|---|---|
| **Conductor persona** | `orchestrator/internal/conductor/persona.go` | `core_identity.yaml`, `safety_boundaries.yaml`, `methodology.yaml`, `output_schema.json` | The single-command chat engine (`conductor.Engine`) — one-shot proposals, not the multi-step agent loop |
| **Agent persona** | `orchestrator/internal/agent/persona.go` | `agent.yaml` | The autonomous task loop (`agent.Runner`) — see [Agent Overview](../agent/overview.md) |

They're deliberately kept apart because the two subsystems have genuinely different jobs: the conductor is answering "what's the single next command for this request," while the agent is running a whole graph-driven task across many steps with its own state. A change to one persona has no effect on the other unless you explicitly also update it — which is a real trap to know about: editing `safety_boundaries.yaml` alone does **not** change the agent loop's behavior, since the agent loop never reads that file.

## The conductor persona — three files, one struct <a id="conductor-persona"></a>

`core_identity.yaml` sets the values (`user_authority`, `privacy_first`, `minimal_footprint`, `transparency`, `ethical_only`) and the character/role prose. `methodology.yaml` is a plain ordered list of reasoning steps (clarify objective → determine engagement phase → pick minimum-footprint technique → classify risk honestly → present with rationale → interpret and propose next step) consumed directly as `Methodology []string` — there's no nested structure to it beyond the list itself. All three files merge into one in-memory persona struct at startup; there's no runtime re-read, so a YAML edit needs an orchestrator restart to take effect.

## The agent persona — one file, model-independent by design <a id="agent-persona"></a>

`agent.yaml`'s own header comment states its design constraint more explicitly than almost any other file in this repository, and it's worth quoting directly because it's the whole philosophy in one sentence: *"Deliberately model- and context-INDEPENDENT: it encodes a WAY OF REASONING — the disposition and guardrails of a disciplined expert operator — not tool names, not flags, not canned procedures, not answers. Strengthen the reasoning here; never script actions."* Concretely, this means the file's `identity` prose tells the model it's operating fully inside an isolated, authorized sandbox and should never self-censor a local action out of unwarranted caution — but it does not, and should not, contain tool-specific procedures. Tool-specific procedural knowledge is exactly what [skills](../agent/skills-and-subagents.md#skills) exist for — the persona shapes *disposition*, skills shape *approach for a class of target*, and neither is supposed to duplicate the other's job.

## Safety boundaries — read this one in full <a id="safety-boundaries"></a>

`safety_boundaries.yaml`'s own header comment states the philosophy behind every rule in it: the operator is authorized and works inside an isolated sandbox whose network egress is enforced at a *lower layer* (the gateway) — so **the persona's job is not to refuse-by-default**; execution oversight is the operator's own job via the approval mode, not something the persona should try to duplicate by being cautious. Two rules stand out as worth understanding precisely, because they're easy to misread as contradictory at a glance:

- **A HARD RULE requiring a named target** — if a request is about scanning/probing a network target but doesn't name a specific host/IP/domain/file, the *only* valid response is a clarifying question, never a guess, and specifically never a scan proposal against the sandbox's own subnet just because nothing else was named. Phrases like "bu sistem" / "this system" / "check for vulnerabilities" are explicitly called out as **not** meaning "scan yourself" — they mean the operator forgot to name a target.

  `safety_boundaries.yaml` names the exact subnet, `172.20.0.0/24` (sandbox at `172.20.0.10`), matching `docker-compose.yml` and `gateway/config/default.yaml` precisely — this was previously the much broader `172.16.0.0/12` (the whole RFC 1918 block the real subnet happens to fall inside, over a million addresses vs. the sandbox's actual 256), tightened specifically so the model can't misread a legitimate, different in-scope target sharing that same /12 as "myself."
- **Simultaneously, act decisively inside the sandbox once a target *is* named** — don't refuse legitimate reconnaissance, file reads (`/etc/passwd` included), user/process enumeration, or scanning of a reachable host; the whole authorized environment is the operator's workspace, and there's no meaningful distinction to draw between "your own system" and "a target."

These aren't in tension — the first rule is about **not inventing a target that was never given**, the second is about **not refusing a target that was**. Read quickly, "act decisively" and "insist on a named target first" can look like they pull in opposite directions; they don't — the boundary line is specifically about invented targets, not about caution in general.

The scope-enforcement line in this file is the persona-side half of the "ping google.com" fix described in [Security Model § Egress modes](../security-model.md#egress-modes): the model is told network scope is *always and transparently* enforced by the gateway regardless of any configuration it can see, that it should never try to pre-judge reachability itself, and that a `[COMMAND_BLOCKED]` observation means "choose a different target or ask the operator," never grounds to claim it personally lacks permission.

## Golden tests <a id="golden-tests"></a>

`persona/v0.1/golden_tests/*.yaml` are executable input→expected-intent fixtures. `internal/conductor.TestPersonaGoldenFixtures` loads the directory and evaluates each case against the deterministic pre-LLM boundary policy; CI runs it as part of the normal orchestrator test suite. `scope_rejection.yaml`, for example, requires `"Tüm interneti tara"` to produce `decline`, while missing-target cases require `ask_clarification`. Run the contract directly with `cd orchestrator && go test ./internal/conductor -run TestPersonaGoldenFixtures`.

## `tier_capabilities/` — documentation only, NOT loaded at runtime <a id="tier-capabilities"></a>

This is a genuinely easy trap to fall into, and the files say so themselves: every file under `persona/v0.1/tier_capabilities/` (`standard.yaml`, `expert.yaml`) opens with an explicit comment that it is **reference documentation only** — the real model tag, context window, and thinking-mode values are hardcoded in `orchestrator/internal/conductor/tier.go`'s `DefaultTierRegistry()` (see [Installation § Ollama setup](../installation.md#ollama-setup) for that table). **Editing only a `tier_capabilities/*.yaml` file changes nothing at runtime.** If you need to change what model a tier actually uses, you have to change `tier.go` — these YAML files then need to be updated *by hand* to stay descriptive of reality, and nothing enforces that they will be. Treat a mismatch between one of these files and `tier.go` as the YAML file being stale, not the other way around.

## Load failures degrade the two paths differently <a id="load-failure"></a>

The two loaders fail differently today. `conductor.NewEngine` catches a conductor-persona load error, logs `persona load failed; continuing with empty persona`, and keeps chat enabled with an empty persona. By contrast, an `agent.yaml` error disables the agent runner/task loop during startup. This asymmetry is important for security review: a malformed conductor file can remove document-driven constraints without disabling model calls, while the agent path fails unavailable. After fixing either file, restart orchestrator because personas are loaded only during wiring.
