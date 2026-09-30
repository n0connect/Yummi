# yummi-brain

A frontier-model agent brain (LangGraph) that drives Yummi's fail-closed
pentest substrate — the Rust gateway and Kali sandbox — over MCP, instead of
the Go orchestrator's own local-model backends (`graph`/`eino`).

## Why this exists

The Go backends (`orchestrator/internal/agent`, `orchestrator/internal/agent/eino`)
were built to make local models (qwen3, gpt-oss) reliably complete multi-part
pentest goals. That effort hit a real, repeated ceiling — see the commit
history around `orchestrator/internal/agent/eino/runner.go` for the trail of
fixes (goal-structure preservation, checklist injection, repeated
finish-coverage auditing) and where it still falls short. `yummi-brain` is
the other side of the same problem: instead of continuing to fight a small
local model's goal-tracking, let a mature agent framework and a frontier
model (via OpenRouter or any OpenAI-compatible endpoint) do that part
natively, and keep Yummi's actual differentiator — the fail-closed gateway,
the single audited execution path, the Kali sandbox — as the substrate
underneath.

## Architecture

```
LangGraph (supervisor + specialist roster, loaded from persona/v0.1/subagents/*.yaml)
  recon / web / osint / exploiter / source-analysis / reverser
        │  MCP (stdio)
        ▼
mcp_server.py  — yummi_exec, yummi_get_scope, yummi_request_egress,
                  yummi_record_finding, yummi_list_findings,
                  yummi_list_skills, yummi_list_tactics, yummi_get_context,
                  yummi_read_gateway_audit
        │  HTTP (existing REST API, unchanged)
        ▼
orchestrator (Go)  — internal/executor.RunScopedCommand: the SAME
                      fail-closed, gateway-authoritative execution path
                      the graph/eino backends already use
        ▼
Rust gateway (nftables) ──▶ Kali sandbox
```

This package never opens a connection to the gateway socket or the sandbox
container directly. Every tool call is a REST call to the orchestrator's
existing API (`orchestrator/internal/api/routes.go`) — no new execution
path, no new attack surface beyond this process itself and its model
provider.

The MCP server (`mcp_server.py`) is a standalone artifact any MCP client can
connect to — Claude Desktop, Cursor, Windsurf, OpenHands, Cline, or a custom
agent — not just this package's own LangGraph brain.

## Setup

```bash
cd brain
uv sync
cp .env.example .env   # fill in YUMMI_BRAIN_MODEL_API_KEY
```

Requires the orchestrator + gateway + sandbox already running
(`make start` from the repo root, or at minimum
`docker compose up -d gateway sandbox` plus `go run ./cmd/orchestrator`
from `orchestrator/`).

## Run

```bash
uv run yummi-brain "203.0.113.10 hedefinde ... 1) ... 2) ... 6) ..."
```

Or run the MCP server standalone (for a different MCP client, e.g. Claude
Desktop's config):

```bash
uv run yummi-mcp-server
```

## Status

MVP. `graph.py`'s specialist roster is loaded at runtime from
`persona/v0.1/subagents/*.yaml` (`persona.py`'s `load()`, same source of
truth the `graph`/`eino` Go backends read) — `_FALLBACK_ROLES` is a
hardcoded 6-role stand-in used only if that directory fails to load, not
the normal path. Yummi's Go-side `internal/knowledge` two-layer memory
(persistent context + procedural tactics) and its declarative skills are
exposed as MCP tools (`yummi_list_skills`/`yummi_list_tactics`/
`yummi_get_context`) and folded into the planner's prompt once per run
(`_planner_node`) — a one-shot injection at planning time, not LangGraph's
own per-turn checkpointer.

Two real, current limitations: reads only — there's no tool for a
specialist to write new persistent context or promote a skill mid-run
(`rest_client.py` is deliberately read-only there: those writes stay
in-process in Go's own `agent`/`skillcurator` packages, behind their
sanitize+threatscan+operator-approval pipeline; `yummi_record_finding` is
this agent's actual durable output). And unlike the `graph`/`eino` Go
backends, a brain-driven task's trajectory is never handed to
`skillcurator` for auto-drafting, so a run here doesn't grow Yummi's skill
library the way a `graph`/`eino` run does. The `graph`/`eino` Go backends
remain available and untouched (`YUMMI_AGENT_BACKEND=graph|eino`) — this is
an additional way to drive the same substrate, not a replacement.
