← [Back to README](../README.md) · [Docs index](README.md)

# Usage Guide

Yummi is managed entirely through **one web UI** (nginx-served React app, port 3000) talking to the orchestrator's REST/WS API (see [API Reference](api-reference.md)) — there is no alternative operator surface. This page walks the web UI's six tabs, then the CLI helper binary, in the order you'll actually use them.

## Table of contents

- [Starting a session](#starting-a-session)
- [The six UI tabs](#six-tabs)
  - [Agent](#tab-agent)
  - [Bash](#tab-bash)
  - [Trace](#tab-trace)
  - [Logs](#tab-logs)
  - [Gateway](#tab-gateway)
  - [Health](#tab-health)
- [Approval flows — what you'll actually be asked to decide](#approval-flows)
- [The CLI](#the-cli)

---

## Starting a session <a id="starting-a-session"></a>

The preferred local operator workflow is just **`make start`** (see
[Installation](installation.md#full-stack)): it starts the runtime and the UI
is served at **http://localhost:3000**. The root-module `yummi` binary's
default (no-flag) behavior opens that same URL in an app-style browser window
for convenience — see [The CLI](#the-cli) — but the web UI at that URL is the
one operator surface either way. Every conversation and task belongs to a
**session** — a session groups the chat history, discovered targets,
findings, and the agent tasks run within it.
Creating one is implicit the first time you send a message; you don't need to
explicitly provision it first.

---

## The six UI tabs <a id="six-tabs"></a>

The top status bar has six tabs, all talking to the same orchestrator: **Agent · Bash · Trace · Logs · Gateway · Health**.

### Agent <a id="tab-agent"></a>

This is where you talk to Yummi in natural language, and it's also where the distinction between the two request shapes described in [Architecture § Data flows](architecture.md#flow-single-command) becomes visible in practice:

- A **specific, narrow ask** ("run an nmap scan against 10.0.0.5") typically gets you a single **proposal** — one exact command, with its rationale and risk level shown before it runs. You approve or reject it directly from the chat.
- A **broader goal** ("enumerate this host and tell me what's exploitable") triggers the agent's **task loop** — a numbered plan appears, and you watch it work through steps. In `ask_permission` it pauses before every command; in `auto` it runs commands without that gate. It can still pause for egress, suspicious output, or a decision it cannot make. See [The Agent Graph](agent/graph-and-nodes.md) for exactly what's happening under the hood while a task runs.

**Agent approval mode** (a small toggle, not to be confused with gateway's egress mode — they're genuinely different axes, see [Security Model § Egress modes](security-model.md#egress-modes) for the full distinction): `auto` runs command proposals without waiting for a click; `ask_permission` gates every command regardless of risk. Risk remains displayed for operator context. Read/set the mode via `GET`/`POST /api/v1/agent/mode`.

### Bash <a id="tab-bash"></a>

A live, WebSocket-backed terminal directly into the sandbox container (`/ws/terminal`) — for when you want to type commands yourself rather than asking the agent to. It is `docker exec` under the hood and its packets still cross gateway's nftables policy, but it does **not** run orchestrator's pre-exec target extraction or hostname-policy check. When hostname restrictions matter, prefer agent/direct-exec paths and keep gateway in `block` or `ask`; input files and shell-expanded targets are opaque to name-level policy.

### Trace <a id="tab-trace"></a>

The observability window into what the agent has done and learned. Reading Trace top to bottom reconstructs the public task/activity story for a session:

| Section | What it shows | Backend source |
|---|---|---|
| **Activity timeline** | Every plan update, command, command result, memory recall, skill application, sub-agent handoff (in/out), finding, ask, and finish — nested under their parent task when a hand-off produced them | `GET /sessions/{id}/activity` |
| **Findings** | Structured security findings, color-coded by severity (info → critical) | `GET /sessions/{id}/findings` |
| **Tactics** | Target-scrubbed procedural memory — "this command shape worked / failed / was inconclusive," reusable across sessions since it carries no target value | `GET /tactics` |
| **Context** | Persistent session memory the agent explicitly chose to `REMEMBER`; operators can explicitly forget an entry | `GET /sessions/{id}/context`, `DELETE /sessions/{id}/context/{key}` |
| **Skills** | The currently loaded declarative playbooks (see [Skills & Sub-agents](agent/skills-and-subagents.md)), plus any **pending** curator-drafted skills awaiting your explicit approve/reject | `GET /skills`, `GET /skills/pending` |
| **Roles** | The available specialist sub-agent roles the model can hand off to | `GET /agent/roles` |

The activity feed uses a small glyph per event kind (◇ plan, › command, ✓ result, ○ memory recalled, ◆ skill applied, ↗ handoff started, ↙ handoff completed, ! finding, · context saved, ? ask, ■ finish) — a nested `└` prefix marks an event that happened inside a sub-agent's delegated subtask rather than the top-level agent. Command/handoff/memory-recall rows show a small, fixed vocabulary ("Command dispatched", "Relevant session context applied", ...) rather than the raw command text or model rationale — that detail is intentionally not surfaced on the public activity feed (see [Security Model § Content layer](security-model.md#layer-content)); findings, asks, and the finish summary are the rows that do show the actual model-authored text.

**Pending skills need your explicit decision.** Drafts remain quarantined until an operator approves or rejects them. Approval re-scans the YAML, atomically persists it under `YUMMI_AUTO_SKILL_DIR`, and hot-loads it into the current registry. The shipped Compose deployment keeps the source `./persona` mount read-only and gives the orchestrator a separate writable `orchestrator_data` volume for approved generated skills; an activation or file-write failure returns the draft to retryable `pending` instead of marking it approved. Review the draft as carefully as a code change—the automated content scan is only a floor.

### Logs <a id="tab-logs"></a>

A live server-sent-events stream of orchestrator's own debug log (`GET /api/v1/system/logs`) — useful when something doesn't look right in the UI and you want to see what orchestrator itself logged around that moment, without shelling into the container to tail a file.

### Gateway <a id="tab-gateway"></a>

The operator console for the network trust boundary — this tab exists specifically because gateway runs isolated inside its own container with no UI of its own, and an operator needs a way to see and manage it without dropping to `docker exec`. It shows:

- **Status** — whether gateway is reachable; the displayed egress mode is read separately from `GET /api/v1/gateway/egress-mode`.
- **Active rules table** — every currently-installed nftables allow/block rule, tagged by source (`config` / `agent` / `manual` — see [Security Model § Rule sources](security-model.md#rule-sources)), with a manual add-rule form (network, TTL, and a **mandatory reason** — friction that's intentional, so a manual rule always carries a paper trail of why it exists).
- **Audit log** — the tamper-evident, hash-chained record of every scope decision and rule change (see [Security Model § Audit](security-model.md#layer-audit)).
- **Fingerprint card** — shows whether TTL normalization / MAC randomization are currently enabled (config-level, read-only from this tab — see [Security Model § Fingerprint minimization](security-model.md#fingerprint-masking)).
- **Egress-mode selector** — the live Block/Allow/Ask 3-way switch. **Remember this resets to Block on every gateway restart** — don't be surprised to find it back at the safe default after a `make start`.

### Health <a id="tab-health"></a>

Time-series charts (5-minute / 15-minute / 1-hour windows) of orchestrator's own runtime vitals: Go heap size, Ollama round-trip latency (shown as "offline" rather than a bogus number when Ollama can't be reached), and live goroutine count — plus a module status panel summarizing whether each backing component (Docker, gateway, sandbox, Ollama) currently looks healthy. This is the tab to check first if something feels slow or stuck, before assuming it's a bug rather than, say, Ollama being genuinely under-resourced for the tier you've selected (see [Installation § Ollama setup](installation.md#ollama-setup)).

---

## Approval flows — what you'll actually be asked to decide <a id="approval-flows"></a>

You'll encounter three distinct kinds of "please decide" moments, and they look similar but mean different things:

1. **Command approval** (`ask_permission` mode) — a specific command is about to run; you see it, its rationale, and its risk level, and approve or deny. In `auto`, the UI resolves the proposal without waiting for a click. This is the [approval gate](architecture.md#flow-approval-gate) described in Architecture.

2. **Threat-scan hold** — a command already ran and its **output** tripped a content-layer threat pattern (see [Security Model § Content layer](security-model.md#layer-content)); you're shown the flagged output before it is allowed back into the model's context. This is a different situation from #1 — the command wasn't risky, its *result* looked suspicious. Agent tasks pause in the graph's threat hold; a single-command proposal pauses in an explicit output-review card where you either release its sanitized output to the agent or keep it out of the model context.

3. **Egress approval** (Ask mode only) — the gateway itself wants your sign-off before a *new* destination becomes reachable, independent of whether the command targeting it was risky. See [Security Model § Egress modes](security-model.md#egress-modes).

If a single-command chat proposal sits too long, it expires after a 20-minute TTL (`conductor.ApprovalStore`'s purger) rather than lingering as something you might accidentally approve much later without context. Agent-task gates instead live with their task/context lifecycle.

---

## The CLI <a id="the-cli"></a>

```bash
./bin/yummi            # launches the web browser to the UI (default, no subcommand)
./bin/yummi --start     # start the configured sandbox Compose service (and its dependencies)
./bin/yummi --stop      # stop that configured sandbox service
./bin/yummi --status    # check status
./bin/yummi --doctor    # run diagnostics (same checks as `make doctor`)
./bin/yummi --version   # print build metadata (semver, git SHA, build date)
./bin/yummi --help      # usage
```
