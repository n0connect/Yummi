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
LangGraph (supervisor + specialist roster)
  recon / web / osint / exploiter / source_analysis / reverser
        │  MCP (stdio)
        ▼
mcp_server.py  — yummi_exec, yummi_get_scope, yummi_request_egress,
                  yummi_record_finding, yummi_list_findings,
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

MVP. `graph.py`'s roster is hand-mirrored from
`persona/v0.1/subagents/*.yaml` (not generated from it yet). No memory
layer beyond LangGraph's own checkpointer (Yummi's Go-side
`internal/knowledge` two-layer memory/procedural-tactics store is not yet
exposed as an MCP tool — findings are, procedural memory isn't). The
`graph`/`eino` Go backends remain available and untouched
(`YUMMI_AGENT_BACKEND=graph|eino`) — this is an additional way to drive the
same substrate, not a replacement.
