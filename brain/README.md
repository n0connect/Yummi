# yummi-brain

The optional Python/LangGraph brain drives the operator REST API with an independent OpenAI-compatible model configuration. It is a separate client, not a `YUMMI_AGENT_BACKEND` value. Model requests run in this Python process; this is an explicit alternative to keeping inference local in Go/Ollama. Sandbox research HTTP/DNS and pentest command execution still belong in the gateway-routed sandbox.

## Architecture and authority

```text
LangGraph supervisor + specialist roster
  -> src/yummi_brain/mcp_server.py (stdio MCP)
  -> src/yummi_brain/rest_client.py (operator REST API)
  -> orchestrator scoped shell executor -> Docker broker -> Kali sandbox
```

The client does not access the gateway socket or Docker directly. Its MCP tools include `yummi_exec`, `yummi_get_scope`, `yummi_request_egress`, finding list/write, skills/tactics/context reads, gateway audit reads and quarantined skill proposals. Specialist definitions are read from `persona/v0.1/subagents/*.yaml`.

This client requires an operator-authorized API connection. The REST client does not add a bearer token or implement a login protocol: current Compose access is restricted by the actual socket peer and UI ingress policy. Host loopback `http://127.0.0.1:9080` reaches the UI nginx proxy. Do not point an untrusted MCP client at that operator surface.

`yummi_exec` calls the blocking operator `POST /api/v1/sandbox/exec`. It reuses `executor.RunScopedCommand` and the UID1000 shell bridge, but it does not enqueue into `execution.Store`, create Go task steps, or wait on the graph/Eino command gate. The handler records session audit entries. Gateway denial remains enforced; this direct endpoint does not implement execution-store egress approval/retry cards.

`yummi_request_egress` directly requests an operator temporary rule through `/gateway/rules` for a literal IP/CIDR. Its name does not imply a separate human approval wait. The gateway master mode remains authoritative: an added rule does not open `block_all`. Exposing this tool grants the client the corresponding operator rule-mutation authority. It cannot set the master egress mode through its current tool set.

Findings remain recorded claims. Skill trajectories go to `/skills/propose` and the same quarantine/curator flow as Go trajectories; promotion still requires the operator pending-skill approval endpoint. The brain's MCP set has no raw persistent-context write or skill-promotion tool.

## Setup

```bash
cd brain
uv sync
cp .env.example .env
# Configure YUMMI_BRAIN_MODEL_API_KEY and the desired endpoint/model.
```

`.python-version` selects Python 3.14; `pyproject.toml` supports `>=3.11,<3.15`. Every `make brain-*` target uses this project interpreter selection. Do not use Python 3.15 for the current dependency set.

Start the Yummi substrate with `make start`, then from the repository root:

```bash
make brain-run GOAL="Inspect the authorized target and report captured evidence"
make brain-mcp  # standalone stdio MCP server for an operator-authorized client
```

`YUMMI_ORCHESTRATOR_URL` defaults to `http://127.0.0.1:9080`. The independent `YUMMI_BRAIN_MODEL_BASE_URL`, `_NAME` and `_API_KEY` configure inference; a non-empty key is required by `config.load`. `YUMMI_BRAIN_SESSION_ID` defaults to `brain-default`; use a distinct session for each engagement. `YUMMI_BRAIN_CHECKPOINT_DB` selects the LangGraph checkpoint SQLite file, separate from orchestrator storage.

See [agent overview](../docs/agent/overview.md), [architecture](../docs/architecture.md) and [quality/evaluation](../docs/agent/quality-and-evaluation.md) for the Go substrate and its different task lifecycle. Run package tests with `make _test-brain`; historical pass counts are snapshots, not current verification results.
