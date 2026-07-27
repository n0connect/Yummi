← [Back to README](../README.md) · [Docs index](README.md)

# Usage Guide

Yummi has **three interfaces to the same orchestrator API**: a web UI (nginx-served React app, port 3000), a terminal UI (Go binary, `./bin/yummi --tui`), and the raw REST/WS API itself (see [API Reference](api-reference.md)). This page walks the web UI's six tabs, then the CLI/TUI, in the order you'll actually use them.

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
- [The TUI](#the-tui)

---

## Starting a session <a id="starting-a-session"></a>

Once `make start` has the stack up (see [Installation](installation.md#full-stack)), open **http://localhost:3000**. Every conversation and task belongs to a **session** — a session groups the chat history, discovered targets, findings, and the agent tasks run within it. Creating one is implicit the first time you send a message; you don't need to explicitly provision it first.

---

## The six UI tabs <a id="six-tabs"></a>

The top status bar has six tabs, all talking to the same orchestrator: **Agent · Bash · Trace · Logs · Gateway · Health**.

### Agent <a id="tab-agent"></a>

This is where you talk to Yummi in natural language, and it's also where the distinction between the two request shapes described in [Architecture § Data flows](architecture.md#flow-single-command) becomes visible in practice:

- A **specific, narrow ask** ("run an nmap scan against 10.0.0.5") typically gets you a single **proposal** — one exact command, with its rationale and risk level shown before it runs. You approve or reject it directly from the chat.
- A **broader goal** ("enumerate this host and tell me what's exploitable") triggers the agent's **task loop** — a numbered plan appears, and you watch it work through steps autonomously, pausing only when it needs your approval for something risky, or your input for a decision it can't make ("I found two possible attack paths — which one should I pursue?"). See [The Agent Graph](agent/graph-and-nodes.md) for exactly what's happening under the hood while a task runs.

**Agent approval mode** (a small toggle, not to be confused with gateway's egress mode — they're genuinely different axes, see [Security Model § Egress modes](security-model.md#egress-modes) for the full distinction): `auto` lets low-risk commands run without a prompt; `ask_permission` gates every command regardless of risk. Read/set it via `GET`/`POST /api/v1/agent/mode`.

### Bash <a id="tab-bash"></a>

A live, WebSocket-backed terminal directly into the sandbox container (`/ws/terminal`) — for when you want to type commands yourself rather than asking the agent to. This is the *interactive terminal* mentioned in [Architecture](architecture.md): it's `docker exec` under the hood too, same as every agent-run command, so anything you type here is subject to the exact same gateway scope check as an agent-proposed command — **this tab does not bypass the trust boundary**, it's just a different, human-driven way to reach the same sandboxed shell.

### Trace <a id="tab-trace"></a>

The observability window into everything the agent has done and learned — this is the tab most older internal notes about this project don't mention, because it was added after most of that documentation was written. Reading Trace top to bottom tells you the whole story of a session:

| Section | What it shows | Backend source |
|---|---|---|
| **Activity timeline** | Every plan update, command, command result, memory recall, skill application, sub-agent handoff (in/out), finding, ask, and finish — nested under their parent task when a hand-off produced them | `GET /sessions/{id}/activity` |
| **Findings** | Structured security findings, color-coded by severity (info → critical) | `GET /sessions/{id}/findings` |
| **Tactics** | Target-scrubbed procedural memory — "this command shape worked / failed / was inconclusive," reusable across sessions since it carries no target value | `GET /tactics` |
| **Context** | Persistent, never-forgotten memory the agent explicitly chose to `REMEMBER` (e.g. a source-code connection map) | `GET /sessions/{id}/context` |
| **Skills** | The currently loaded declarative playbooks (see [Skills & Sub-agents](agent/skills-and-subagents.md)), plus any **pending** curator-drafted skills awaiting your explicit approve/reject | `GET /skills`, `GET /skills/pending` |
| **Roles** | The available specialist sub-agent roles the model can hand off to | `GET /agent/roles` |

The activity feed uses icons per event kind (🗺️ plan, ⚡ command, ✓ result, 🧠 memory recalled, 📘 skill applied, 🤝 handoff started, ↩︎ handoff completed, 🔎 finding, 💾 context saved, ❓ ask, 🏁 finish) — a nested `└` prefix marks an event that happened inside a sub-agent's delegated subtask rather than the top-level agent.

**Pending skills need your explicit decision.** If you see an entry under "Skills" with an approve/reject action, that's the auto-skill-curation pipeline: the agent noticed a trajectory that worked well and drafted a reusable skill from it, but **no draft is ever auto-loaded** — every one sits quarantined until an operator reviews and approves it (`POST /skills/pending/{id}/approve`) or rejects it (`POST /skills/pending/{id}/reject`). Treat this the same way you'd treat reviewing a pull request before merging it — the draft already passed an automated content-layer threat scan, but that's a floor, not a substitute for you actually reading what it proposes to teach the agent going forward.

### Logs <a id="tab-logs"></a>

A live server-sent-events stream of orchestrator's own debug log (`GET /api/v1/system/logs`) — useful when something doesn't look right in the UI and you want to see what orchestrator itself logged around that moment, without shelling into the container to tail a file.

### Gateway <a id="tab-gateway"></a>

The operator console for the network trust boundary — this tab exists specifically because gateway runs isolated inside its own container with no UI of its own, and an operator needs a way to see and manage it without dropping to `docker exec`. It shows:

- **Status** — is gateway reachable, what's its current egress mode.
- **Active rules table** — every currently-installed nftables allow/block rule, tagged by source (`config` / `agent` / `manual` — see [Security Model § Rule sources](security-model.md#rule-sources)), with a manual add-rule form (network, TTL, and a **mandatory reason** — friction that's intentional, so a manual rule always carries a paper trail of why it exists).
- **Audit log** — the tamper-evident, hash-chained record of every scope decision and rule change (see [Security Model § Audit](security-model.md#layer-audit)).
- **Fingerprint card** — shows whether TTL normalization / MAC randomization are currently enabled (config-level, read-only from this tab — see [Security Model § Fingerprint minimization](security-model.md#fingerprint-masking)).
- **Egress-mode selector** — the live Block/Allow/Ask 3-way switch. **Remember this resets to Block on every gateway restart** — don't be surprised to find it back at the safe default after a `make start`.

### Health <a id="tab-health"></a>

Time-series charts (5-minute / 15-minute / 1-hour windows) of orchestrator's own runtime vitals: Go heap size, Ollama round-trip latency (shown as "offline" rather than a bogus number when Ollama can't be reached), and live goroutine count — plus a module status panel summarizing whether each backing component (Docker, gateway, sandbox, Ollama) currently looks healthy. This is the tab to check first if something feels slow or stuck, before assuming it's a bug rather than, say, Ollama being genuinely under-resourced for the tier you've selected (see [Installation § Ollama setup](installation.md#ollama-setup)).

---

## Approval flows — what you'll actually be asked to decide <a id="approval-flows"></a>

You'll encounter three distinct kinds of "please decide" moments, and they look similar but mean different things:

1. **Command approval** (medium+ risk) — a specific command is about to run; you see it, its rationale, and its risk level, and approve or deny. This is the [approval gate](architecture.md#flow-approval-gate) described in Architecture.
2. **Threat-scan hold** — a command already ran and its **output** tripped a content-layer threat pattern (see [Security Model § Content layer](security-model.md#layer-content)); you're shown the flagged output and can approve it as-is, deny/discard it, or supply a redacted version before it's allowed back into the model's context. This is a different situation from #1 — the command wasn't risky, its *result* looked suspicious.
3. **Egress approval** (Ask mode only) — the gateway itself wants your sign-off before a *new* destination becomes reachable, independent of whether the command targeting it was risky. See [Security Model § Egress modes](security-model.md#egress-modes).

If a proposal or a pause sits too long: command approvals expire after a 20-minute TTL (`conductor.ApprovalStore`'s purger) rather than lingering as something you might accidentally approve much later without context.

---

## The CLI <a id="the-cli"></a>

```bash
./bin/yummi            # launches the web browser to the UI (default, no subcommand)
./bin/yummi --tui       # launches the terminal UI instead
./bin/yummi --start     # start services (equivalent to `make start`)
./bin/yummi --stop      # stop services
./bin/yummi --status    # check status
./bin/yummi --doctor    # run diagnostics (same checks as `make doctor`)
./bin/yummi --version   # print build metadata (semver, git SHA, build date)
./bin/yummi --help      # usage
```

---

## The TUI <a id="the-tui"></a>

`./bin/yummi --tui` (or `make start-tui`, which runs `make start` first) opens a `bubbletea`-based terminal operator console — a genuine second front-end to the same orchestrator API the web UI uses, not a stripped-down subset. It supports the same core loop: send a goal or command, watch the agent's live activity timeline stream in over the WebSocket, and approve or deny gated commands and agent tasks directly from the terminal. Reach for it when you're already living in a terminal (over SSH to a remote box running Yummi, for instance) and don't want to also keep a browser tab open.
