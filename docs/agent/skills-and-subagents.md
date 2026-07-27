← [Back to README](../../README.md) · [Docs index](../README.md) · [Agent overview](overview.md)

# Skills & Sub-agents

Two related but distinct mechanisms make the agent "smarter" without touching its core reasoning loop: **skills** (declarative, trigger-matched knowledge injected into the prompt) and **sub-agents** (bounded delegation to a differently-specialized persona). Both are loaded from plain YAML under `persona/v0.1/`, and both are explicitly designed so that adding one never requires a code change.

## Table of contents

- [Skills — knowledge only, never an execution path](#skills)
- [How a skill gets selected](#skill-selection)
- [A real skill, read in full](#real-skill)
- [Sub-agents — bounded delegation](#subagents)
- [Concurrent hand-off — the fan-out](#concurrent-handoff)
- [Why delegation depth is capped at 1](#depth-cap)
- [Auto-skill-creation — drafted, never auto-loaded](#auto-skill)

---

## Skills — knowledge only, never an execution path <a id="skills"></a>

`internal/skill`'s own doc comment states the constraint plainly: a skill "introduces no execution path; every command the agent runs still flows through the single fail-closed sandbox exec path" (see [Sandbox Execution](sandbox-execution.md)). A `Skill` is exactly four fields:

```go
type Skill struct {
    Name        string
    Description string
    Triggers    []string // lowercase keywords matched against goal + tools
    Tools       []string // recommended sandbox tools (informational)
    Knowledge   string    // rendered verbatim into the prompt when selected
}
```

There's no field here that *runs* anything — `Tools` is informational (a hint about what the skill is about), and `Knowledge` is prose the model reads, not a script the agent executes. This is the same "knowledge-only" discipline the Knowledge Store's memory layers follow (see [Memory & Knowledge](memory-and-knowledge.md)) — every piece of learned or curated content in this system is something the *model* reasons over, never something the *agent* blindly acts on without the model's own decision in between.

## How a skill gets selected <a id="skill-selection"></a>

`Registry.Select(goal, tools, max)` scores every loaded skill by how many of its `Triggers` appear as a case-insensitive substring in `goal + " " + tools joined` — a purely mechanical, deterministic match (score descending, then name ascending as a tiebreak), not an LLM call. This matters for two reasons: it's essentially free (no extra model round-trip per step), and it's fully deterministic, so the same goal always surfaces the same skills in the same order — useful when you're trying to reason about *why* the model behaved a certain way after the fact.

Up to `max` (default 3) selected skills get rendered into the step prompt via `Registry.Render()`:
```
SKILLS (relevant playbooks — apply the matching approach):
- recon: Enumerate before you exploit. Start passive/light, escalate only as needed: ...
```

## A real skill, read in full <a id="real-skill"></a>

`persona/v0.1/skills/recon.yaml`, verbatim:

```yaml
name: recon
description: Host and network enumeration — discover what is alive and what it runs.
triggers:
  - recon
  - reconnaissance
  - enumerate
  - enumeration
  - scan
  - ports
  - port
  - nmap
  - discover
  - footprint
tools:
  - nmap
  - rustscan
  - masscan
  - ping
knowledge: |
  Enumerate before you exploit. Start passive/light, escalate only as needed:
  1. Confirm reachability, then `nmap -sV -sC <target>` for service + version + default scripts.
  2. For wide ranges, a fast port sweep first (rustscan/masscan), then targeted -sV on open ports.
  3. Record every open port and service version as a structured finding (it becomes shared memory).
  4. Avoid `-A`/aggressive timing on production unless the engagement allows it — it is loud.
```

Notice what this file is actually teaching: not "run `nmap`" (the model already knows nmap exists — `persona/v0.1/agent.yaml`'s own identity prose is explicit that the model is "given no catalogue and need[s] none" of *tool* knowledge), but a specific **sequencing discipline** ("confirm reachability, then service+version, escalate breadth-first") and an explicit **operational judgment call** ("avoid aggressive timing on production unless the engagement allows it"). Skills exist to encode this kind of house methodology and judgment, not tool syntax.

**Every skill file is content-scanned at load time**, same [threatscan](../security-model.md#layer-content) at `ScopeStrict` applied to `name+description+knowledge` together. A skill that trips a pattern is simply not loaded (logged as a warning, never a hard crash of the whole registry) — a sensible posture given a skill file is user-curated content that gets rendered verbatim into every matching step's prompt.

## Sub-agents — bounded delegation <a id="subagents"></a>

A `SubAgentDef` (`persona/v0.1/subagents/*.yaml`) is a full alternate persona — `role`, `identity`, `principles`, `planning` prose, plus a `skills` list scoping which skills that role can see. At wiring time each becomes its own `agent.Runner` — **the same graph state machine** described in [Graph & Nodes](graph-and-nodes.md), just specialized by a different persona and skill subset, sharing the parent's `Store` and `Knowledge` — not a different, simpler mechanism bolted on for delegation.

`persona/v0.1/subagents/recon.yaml`, verbatim:

```yaml
role: recon
identity: |
  You are a reconnaissance specialist sub-agent inside an isolated Kali sandbox.
  You enumerate hosts and services thoroughly and report structured findings.
  You do exactly the focused subtask handed to you, then finish — you do not
  expand scope or start exploitation.
principles: |
  - Enumerate before anything else; breadth first, then depth on what is open.
  - Every open port/service version is a structured finding (it becomes shared memory).
  - Stay light and quiet unless the subtask explicitly calls for aggressive scanning.
planning: |
  Produce a short ordered plan that discovers reachability, then services, then
  versions for the handed subtask. Keep it to a few concrete steps.
skills:
  - recon
```

The identity prose's last line — *"you do exactly the focused subtask handed to you, then finish — you do not expand scope or start exploitation"* — is the behavioral contract that makes delegation safe to use at all: a recon sub-agent is explicitly told not to wander into exploitation even if it notices something exploitable mid-task, keeping the blast radius of a single hand-off narrow and predictable.

## Concurrent hand-off — the fan-out <a id="concurrent-handoff"></a>

`HANDOFF` supports two shapes in one action: a single target (`{"role","subgoal"}`) or a fan-out (`{"handoffs":[{"role","subgoal"}, ...]}`) that dispatches **multiple specialists concurrently**, on one shared deadline for the whole batch. `handoffNode` (`node_handoff.go`) launches one goroutine per target and `sync.WaitGroup`s them together — the wall-clock cost of delegating to three specialists at once is roughly the cost of the *slowest* one, not the sum of all three, and each specialist's failure (including a timeout) is folded into the merged observation individually rather than failing the whole hand-off if one target struggles.

Every sub-agent shares the same Knowledge Store as the parent — a finding a recon sub-agent records is immediately visible to the parent's own memory queries, with no separate merge step required, because there was only ever one store to begin with. Only a **human-readable summary** (not the sub-agent's full internal reasoning trace) gets threaded back into the parent's next prompt — this keeps a hand-off cheap in context-window terms regardless of how much work the sub-agent actually did internally.

A sub-agent's summary text passes through the exact same content-layer defense as any other tool output before it reaches the parent's prompt (`threatscan.StripANSI` + `Banner()` at `ScopeContext`) — because a sub-agent's own LLM output can itself echo injected content it read from a target while doing its delegated work, which is exactly the same risk class regular tool output carries. This is deliberately built directly into `handoffNode`, not inherited automatically from `observeResult` — a hand-off's merged observation is assembled by hand (`handoffNode` builds `s.lastObs` directly), so without this explicit step, sub-agent output would reach the parent's prompt completely raw.

## Why delegation depth is capped at 1 <a id="depth-cap"></a>

A sub-agent's own `Runner` is built with an **empty** `SubAgents` map — so a sub-agent that itself tries to `HANDOFF` simply has nowhere to delegate to. This isn't a runtime check catching infinite recursion after the fact; it's structurally impossible for recursion to happen at all, because the data a second-level hand-off would need to route through was never populated. A recon specialist genuinely cannot accidentally (or via a successful prompt injection) spawn a chain of nested delegations that could balloon cost or complexity unpredictably — the depth-1 limit is a property of the object graph, not a policy someone remembered to enforce.

## Auto-skill-creation — drafted, never auto-loaded <a id="auto-skill"></a>

When a task completes, `proposeSkillFromTrajectory` (`node_lifecycle.go`, fired from `finishNode`) optionally hands the task's own trajectory — goal, summary, observations, and any `worked`-outcome tactics touching the tools it used — to a background curator (`s.cfg.SkillCurator`), detached from the task's own (already-cancelled) context and dispatched via `go` so a slow curator LLM call never delays the task's own completion. If the curator decides the trajectory is worth turning into a reusable playbook, it drafts one and writes it to a **quarantined pending-skills store** — visible in the Trace tab's Skills section, exactly like the [usage guide](../usage-guide.md#tab-trace) describes.

**No draft is ever auto-loaded into the live registry.** Every draft passes through the same `threatscan.FirstThreatMessage(..., ScopeStrict)` scan hand-authored skill YAML already goes through at `LoadDir` time — reusing the existing content-layer invariant rather than inventing a parallel one — and then sits quarantined until an operator explicitly approves it (`POST /skills/pending/{id}/approve`, which writes it into the real `persona/v0.1/skills/` directory `LoadDir` already watches, and adds it to the live in-process registry immediately) or rejects it. This mirrors a deliberate design principle stated directly in this project's own roadmap notes: "every skill passes mandatory review before publishing, not an open registry" — the agent can *suggest* new knowledge for itself, but never *grant itself* new knowledge without a human in the loop.
