[Back to Project README](../../README.md) · [Docs index](../README.md)

# Agent overview

Yummi translates goals into plans and sandbox tool calls. The model proposes actions; the orchestrator records execution, the operator controls approvals, and the gateway controls forwarded network traffic. A completed task or a fluent summary alone does not establish that the goal was achieved.

## Backends and execution

`YUMMI_AGENT_BACKEND=graph` selects the Go graph state machine; `eino` selects the Go Eino ADK tool-calling backend. Both implement `TaskRunner`, share the task/knowledge stores and scoped execution substrate, and support specialist delegation. Specialists use the selected backend and have no specialist registry of their own, limiting delegation to one level.

The optional [Python brain](../../brain/README.md) is a separate LangGraph client of the operator REST API, not another `YUMMI_AGENT_BACKEND` value. Its operator API authority and lifecycle differ from graph/Eino task execution.

Local inference uses Ollama. An explicitly configured OpenAI-compatible base URL (`YUMMI_EINO_MODEL_BASE_URL`) also supplies the conductor and graph model client, despite the `EINO` prefix; Eino uses it for native tool calling. Provider selection alone does not supply an endpoint or credentials. See [configuration reference](../configuration.md).

The tier registry defaults to `gpt-oss:20b` for `standard` and `gpt-oss:120b` for `expert`; an explicit model overrides the preset. These are configuration defaults, not guarantees about model competence. Historical lab observations in `conductor/tier.go` do not establish a universal parameter-count floor or current provider recommendation.

Graph actions use the closed vocabulary in `agent/protocol.go`, including `[RUN_COMMAND]`, `[MCP_TOOL]`, `[HANDOFF]`, `[REMEMBER]`, `[SKILL_VIEW]`, `[FINISH]` and `[ASK_OPERATOR]`. Eino exposes native tools instead. The sandbox tool schemas come from MCP discovery; use the installed server's `tools/list`, rather than a fixed count in documentation.

Graph/Eino shell and MCP calls enqueue into `execution.Store`. The executor either runs a scoped shell through `sandbox.Bridge` or dispatches to the installed sandbox MCP server through a persistent Docker exec transport. Both run as UID/GID `1000:1000`. Agent code does not spawn host processes. The separate operator `/sandbox/exec` endpoint reuses the scoped shell path directly; it is not an agent task or an execution-store enqueue.

## Independent approvals

Command approval and network permission are independent:

- In `ask_permission`, graph/Eino command calls require approval. A per-task `auto-approve` override skips only non-high-severity commands without a forced approval reason.
- In `auto`, ordinary risk labels do not themselves force a command approval. Structural destructive floors (`ForceApprovalReason`) still require it.
- Threat output has its own hold with `pending_approval_kind=threat`; approving a command does not authorize its output.
- Gateway `block_all`, `block_all_except_rules`, `allow_all` and `ask_every_time` determine egress. An execution may wait separately in `awaiting_egress_approval`. Gateway restart resets the mode to `block_all`.

Automated approval clients must verify task/session/proposal identity and exact `pending_mcp_tool`/`pending_mcp_args`; display command text is not executable authority.

## Operator events and evidence

The initial chat POST starts a task and returns its identity; it does not wait for the final report. The UI follows `task.status`, `execution.status` and `agent.activity` envelopes on `/ws/events` and loads persisted state/history. Envelope `event` identifies lifecycle subtypes such as `execution.running` and `execution.completed`.

Shell executions support incremental `execution.output` chunks as well as running heartbeats. Output chunks are live-only; lifecycle replay is bounded and scoped by a process epoch and monotonically increasing sequence. Clients must resync from REST after an epoch change or replay gap. MCP calls generally return their completed result rather than shell output chunks.

Finding verdicts check whether quoted evidence is traceable to successful tool output. Oracle labels and active-proof helpers use bounded heuristics, not independent proof of exploitation. See [quality and evaluation](quality-and-evaluation.md) and [execution provenance](execution-provenance.md).

## References

| Area | Reference |
|---|---|
| Graph nodes, recovery and loop guards | [Graph & Nodes](graph-and-nodes.md) |
| Control tags and parsing | [Tag protocol](tag-protocol.md) |
| Notes, tactics and asset records | [Memory & Knowledge](memory-and-knowledge.md) |
| Playbook selection and delegation | [Skills & Sub-agents](skills-and-subagents.md) |
| Chromium sessions and networking | [Browser automation](browser-automation.md) |
| Public search/fetch and evidence limits | [Web research](web-research.md) |
| Docker execution and MCP discovery | [Sandbox execution](sandbox-execution.md) |
| Runtime placement | [Sandbox confinement](sandbox-confinement.md) |
| Dated verification evidence | [QC history](web-research-qc.md) · [17 October deployment snapshot](live-deployment-2026-10-17.md) |

Source: `orchestrator/internal/agent/task_runner.go`, `graph.go`, `node_tool.go`, `eino/pentest_tools.go`, `orchestrator/cmd/orchestrator/main.go`, `internal/api/event_hub.go` and `handlers_system.go`.
