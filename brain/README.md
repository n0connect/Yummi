# yummi-brain

A frontier-model agent brain (LangGraph) that drives Yummi's fail-closed pentest substrate — the Rust gateway and Kali sandbox — over MCP, instead of the Go orchestrator's own local-model backends (`graph`/`eino`).

---

## Why This Exists

The Go backends (`orchestrator/internal/agent`, `orchestrator/internal/agent/eino`) were built to make local models (`qwen3`, `gpt-oss`) reliably complete multi-part pentest goals. `yummi-brain` is the frontier alternative: instead of pushing a small local model to its reasoning ceiling, an external LangGraph supervisor/specialist multi-agent system powered by frontier models (Claude 3.5 Sonnet, GPT-4o, DeepSeek-V3) drives Yummi's core differentiator — the fail-closed gateway, single audited execution path, and Kali sandbox.

---

## Architecture

```text
LangGraph (supervisor + specialist roster, loaded from persona/v0.1/subagents/*.yaml)
  recon / web / api-hunter / js-miner / auth-auditor / cloud-recon / osint / exploiter / source-analysis / reverser
        |  MCP (stdio)
        v
mcp_server.py  -- yummi_exec, yummi_get_scope, yummi_request_egress,
                  yummi_record_finding, yummi_list_findings,
                  yummi_list_skills, yummi_list_tactics, yummi_get_context,
                  yummi_read_gateway_audit, yummi_propose_skill
        |  HTTP (existing REST API, unchanged)
        v
orchestrator (Go)  -- internal/executor.RunScopedCommand: the SAME
                      fail-closed, gateway-authoritative execution path
                      the graph/eino backends already use
        v
Rust gateway (nftables) ---> Kali sandbox
```

This package never connects to the gateway socket or sandbox container directly. Every tool call is an authenticated REST call to the orchestrator's existing API (`/api/v1`) — zero new execution paths or attack surface.

The MCP server (`mcp_server.py`) is a standalone artifact any MCP client can connect to — Claude Desktop, Cursor, Windsurf, OpenHands, Cline, or custom external agents.

---

## Setup

```bash
cd brain
uv sync
cp .env.example .env   # fill in YUMMI_BRAIN_MODEL_API_KEY
```

Python 3.14 is selected by `.python-version`; the package supports Python 3.11–3.14.
The upper bound excludes Python 3.15 until native dependencies support it.
`uv sync` and every `make brain-*` target use this project selection.

Requires orchestrator + gateway + sandbox running (`make start`).
