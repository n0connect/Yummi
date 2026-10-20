[Back to Project README](../../README.md) · [Docs index](../README.md) · [Agent overview](overview.md)

# Skills and sub-agents

Skills provide declarative methodology; specialist roles provide bounded task delegation. Neither creates a new execution authority. Commands from the main agent and specialists use the same sandbox execution and gateway policies.

## Skill artifacts <a id="skills"></a>

Built-in playbooks live in `persona/v0.1/skills/*.yaml`. Operator-approved generated playbooks live under `YUMMI_AUTO_SKILL_DIR` (default `$YUMMI_HOME/skills`) and merge into the registry at startup. Use `GET /api/v1/skills` for the actually loaded names, versions and provenance rather than a fixed source-file count.

A skill has `schema_version`, semantic `version`, `name`, `description`, `triggers`, informational `tools`, `knowledge`, optional `references`, `plan_step_limit` and provenance. Legacy files normalize to schema 1/version `1.0.0`. The registry computes a content digest, validates shape/limits and strict-scans name/description/knowledge before loading. Invalid files are skipped with warnings. `tools` recommends tools; it is not an executable allowlist or a grant of permission.

## Selection and progressive loading <a id="skill-selection"></a>

`Registry.Select(goal, tools, max)` counts case-insensitive trigger substring matches against the goal plus tool names, then sorts by score descending and name ascending. A non-positive `max` defaults to three. This deterministic suggestion algorithm does not prove a playbook applies.

Planning receives the skill index plus selected bodies and reviewed plan constraints. Step prompts carry a compact `SKILLS` index, with up to five suggested descriptions and other names, bounded by the default 3,500-byte index budget. They do not automatically repeat every selected full playbook on every turn.

The graph `[SKILL_VIEW] {"name":"web"}` action (`node_skill.go`) and Eino `skill_view` tool load a full playbook by exact name. Its use produces an `ActSkill` trace activity. Unknown names return a recoverable tool failure. `Registry.Subset` limits a specialist's available playbooks to its configured skills; unknown subset names are ignored.

The source directory includes web/API, authentication, AD, cloud, network, binary, hardware/RF and adversary-emulation methodology. A playbook describing a technique does not establish that required software, hardware or network access exists in the running sandbox.

## Specialist roles <a id="subagents"></a>

Role YAML lives in `persona/v0.1/subagents/` and is loaded by `LoadSubAgentDefs`. Each definition supplies identity/principles, skill names, routing hints and a step budget. The main process builds graph specialists for a graph parent and Eino specialists for an Eino parent; they share stores and execution dependencies. `GET /api/v1/agent/roles` describes the loaded roster. Use exact registered role names instead of relying on a static count.

Example graph action:

```text
[HANDOFF]
{"handoffs":[
  {"role":"api-hunter","subgoal":"Inspect the authorized API schema for access-control test cases"},
  {"role":"js-miner","subgoal":"Identify endpoints in the authorized application's JavaScript"}
]}
```

Eino exposes the corresponding native `handoff` tool. Delegated work creates child tasks with parent identity and separate step budgets. Its command approvals remain visible to the operator.

## Bounds and lifecycle <a id="concurrent-handoff"></a>

Configuration defaults bound a handoff to four targets and a coordinator to three concurrent specialist runs. One coordinator is shared across tasks of the top-level runner; this is not a system-wide quota covering independent orchestrators or the Python brain. The configured parent deadline bounds a fan-out, including time queued for a slot.

The coordinator records run/task identity, role, subgoal, outcome and timing, retains bounded recent history and supports individual interruption. Operators can list delegations and interrupt a run through `/api/v1/agent/delegations` and `/api/v1/agent/delegations/{id}/interrupt`.

Specialists are constructed with an empty `SubAgents` map, bounding built-in delegation to depth one. This prevents recursive handoff through that runner configuration; it does not remove the cost of each allowed child run.

Graph handoff results strip ANSI and append a threat banner for suspicious child summaries before returning them to the parent. That annotation is not the same as the separate blocking `threat_hold` applied to tool output. Findings and persistent notes retain their own knowledge-store validation policies.

## Generated playbooks <a id="auto-skill"></a>

A successful trajectory can be submitted to the bounded background `skillcurator.Worker`. The curator sanitizes target/credential material, drafts methodology, validates schema/content and checks duplicates. Candidates enter the quarantined pending-skill store; they never auto-activate.

`POST /api/v1/skills/pending/{id}/approve` claims the pending record, writes and syncs a temporary YAML file, renames it into the configured auto-skill directory, upserts the live registry and commits promotion. A failed promotion must not be described as approved. Operators can inspect/reject pending proposals through the skill API. The optional brain can submit a trajectory to `/skills/propose` but cannot promote it through its own MCP tool set.

Source: `orchestrator/internal/skill/skill.go`, `internal/agent/node_skill.go`, `node_handoff.go`, `delegation.go`, `internal/agent/eino/skill_tool.go`, `internal/skillcurator/`, `internal/api/routes.go` and `orchestrator/cmd/orchestrator/main.go`.
