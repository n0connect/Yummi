# Documentation reconciliation — started 2026-10-17, completed 2026-10-20

This is a source-based documentation audit of the current working tree. It does not certify a running deployment. Maintained guides were compared with executable source, Make targets, Compose/Dockerfiles, runtime defaults, handlers and wire schemas. Dated plans/results retain their original findings with an explicit historical-record notice.

## Corrections

| Area | Corrected description | Implementation evidence |
|---|---|---|
| Services and topology | Five default services; repository-built constrained Go Docker broker; internal/control/ingress/external networks; both host loopback ports terminate at UI nginx. | [Compose](../docker-compose.yml), [nginx](../ui/nginx.conf), [broker](../orchestrator/internal/dockerproxy/broker.go) |
| Builds and startup | `make start` reuses existing images; use `BUILD=1` after source/language changes. Native outputs do not feed Docker builds. Go modules require 1.27.2. | [Makefile](../Makefile), [root module](../go.mod), [orchestrator module](../orchestrator/go.mod), [sandbox module](../sandbox-mcp/go.mod) |
| Configuration | Separate Make, native runtime, Compose, gateway YAML and Brain layers; explicit model beats tier; wizard selects Eino while raw backend default is graph. | [Configuration reference](configuration.md), [config loader](../orchestrator/internal/config/config.go), [startup](../orchestrator/cmd/orchestrator/main.go) |
| Model routing | An explicit compatible base URL supplies conductor/graph as well as Eino; hosted inference/search sends data to configured providers. Model results and size thresholds are not universal guarantees. | [Startup](../orchestrator/cmd/orchestrator/main.go), [Brain config](../brain/src/yummi_brain/config.py), [research](../sandbox-mcp/research_tools.go) |
| Gateway lifecycle | Startup uses `block_all`; daemon crash preserves last committed kernel rules while the namespace survives. Temporary permissions still have kernel TTLs. Restart resets mode. Same-subnet/local traffic is outside the forwarding boundary. | [Gateway startup](../gateway/src/main.rs), [nftables](../gateway/src/network/nftables.rs) |
| Approval and risk | Single-command proposals, task command gates, threat holds and network approvals differ. Agent `auto` skips ordinary risk gates; detected destructive patterns force approval. | [Validator](../orchestrator/internal/conductor/validator.go), [graph tool node](../orchestrator/internal/agent/node_tool.go), [Eino pipeline](../orchestrator/internal/agent/eino/pentest_tools.go) |
| API examples | Corrected task `proposal_id`/`approved`, per-task `enable`, scope classifier `hostname`/`hostnames`; added upload, skill usage/proposal and terminal deletion routes. | [Routes](../orchestrator/internal/api/routes.go), [task handlers](../orchestrator/internal/api/tasks.go), [scope handlers](../orchestrator/internal/api/handlers_scope.go) |
| Events and streaming | Real envelope/type/subtype, ephemeral shell output fields, bounded replay and REST resync; command approval is a task state, not an execution status. | [Event hub](../orchestrator/internal/api/event_hub.go), [execution store](../orchestrator/internal/execution/store.go), [shell bridge](../orchestrator/internal/sandbox/bridge.go) |
| Agent/tool documentation | Twelve graph nodes including `skill_view`; real protocol tags; installed MCP schemas as inventory authority; corrected skill loading/delegation and memory limits. | [Graph](../orchestrator/internal/agent/graph.go), [protocol](../orchestrator/internal/agent/protocol.go), [agent guides](agent/overview.md) |
| Evidence claims | Oracles are output-based heuristics; summaries are claims. Program scope warns/classifies rather than silently filtering reports. Proxy ingestion extracts URL assets and is not a preinstalled interception service. | [Oracles](../orchestrator/internal/agent/oracle.go), [report generation](../orchestrator/internal/report/report.go), [proxy handler](../orchestrator/internal/api/handlers_proxy.go) |
| Sandbox and desktop | Root startup/preparation differs from UID1000 tool execution; viewer activation does not stop/start XFCE; documented capability, TUN and writable-mount limitations. | [Entrypoint](../sandbox/entrypoint.sh), [sandbox image](../sandbox/Dockerfile), [Compose](../docker-compose.yml) |
| Labs and diagnostics | Apache 2.4.49/CVE-2021-41773 runs as `daemon`; private CIDRs are configured blocks; lab target bridge/reverse traffic limits and doctor policy mutations are explicit. | [Lab](../lab-target/README.md), [doctor script](../scripts/doctor.sh), [gateway policy](../gateway/src/policy/mod.rs) |

`CLAUDE.md` and `.agent.md` now reference shared `AGENTS.md` rather than maintaining independent architecture copies. Both are already gitignored local files; their edits are not part of the tracked Git diff. No ignore rule was changed.

## Implementation mismatches exposed by the audit

These remain implementation facts, not claims repaired by this documentation change:

- **Legacy host build:** `make build` / `_build-sandbox-mcp` still compile sandbox-MCP on the host, conflicting with mandatory Docker placement. Guides specify granular native component targets and Docker sandbox builds/tests. The target dependency/recipe was not changed.
- **Policy versus packets:** policy evaluation can accept a sufficiently specific manual allow over a configured block, but the kernel rules put configured block DROPs first. `allow_all` policy verdicts also skip configured block evaluation while the packet rules retain configured blocks. A policy/API allow is not proof of packet reachability.
- **Address-class floor:** the policy's unconditional address-class checks and configured nftables block CIDRs are separate; the controller does not automatically inject every policy floor class.
- **TUN setup:** entrypoint attempts `mknod` when `/dev/net/tun` is missing, but Compose omits `MKNOD`. Installed OpenVPN and `NET_ADMIN` alone do not guarantee device creation.
- **Forwarded settings:** some native loader settings, including execution timeout, fallback/embedding model and max-output-token override, are not interpolated by checked-in Compose. The configuration reference identifies that distinction.

Only documentation, configuration comments and Make help text changed. No runtime rules, executable configuration values, API handling, task logic or build recipes changed.

## Verification scope

The final pass checked local Markdown file/anchor links (including historical notices and the two local agent guides), maintained-guide Make references, referenced Go test identifiers and all registered operator HTTP/WebSocket routes against the API reference. `make help NO_COLOR=1` executed successfully. `git diff --check` was run for whitespace errors. Comments-only source/config edits were checked against their original executable content.

Final static results: **66 Markdown files, 463 local link targets, zero link/anchor errors; 45 maintained pages, 208 Make references and all 78 registered HTTP/WebSocket routes, zero contract errors; all 31 registered protocol tags represented.** Go test identifiers mentioned in maintained pages were found in source. `git diff --check` passed. These checks validate documentation contracts, not application behavior.

Application tests, paid providers, sandbox tools, live firewall traffic and deployment recreation were not run in this documentation pass. Existing dated PASS/FAIL records are preserved; they are not fresh results from this audit. External URLs/provider availability were not checked.

Use the [documentation index](README.md) for maintained pages and the history policy, [configuration reference](configuration.md) for effective settings, and [security model](security-model.md) for enforcement limits.
