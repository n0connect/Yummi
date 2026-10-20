# Agent Control Plane Program Design

> **Historical record —** This dated plan/report describes its recorded revision and environment. It is not current implementation or deployment verification. Use the [maintained documentation](../../../docs/README.md) for current behavior. Original findings and results are preserved below.

## Goal

Make Yummi's operator surface, agent runtimes, and Rust gateway behave as one
observable, resumable, fail-closed control plane without moving execution or
network authority into the browser.

## Program boundaries

The work is delivered in four ordered slices:

1. UI to agent turn identity and resumable event delivery.
2. Transactional projection of gateway policy into nftables.
3. Graph and Eino lifecycle parity plus explicit memory, skill, and delegation
   capabilities.
4. An operator cockpit that presents the resulting trust state and task tree.

The Go orchestrator owns sessions, turns, tasks, and API/event delivery. The
Rust gateway remains the only network-policy authority. nftables is the actual
enforcement state. The sandbox remains the only agent-reachable command path.
The UI is an operator console and never becomes an authority.

## Turn and event contract

Every `/api/v1/chat` request carries a client-generated UUID `turn_id`. The
orchestrator permits one active LLM turn per session and returns a typed
`409 turn_in_progress` problem when a second turn races it. Different sessions
remain concurrent. Chat stream events and the final REST response echo the
same turn ID, so another tab cannot paint tokens into the wrong request.

The shared WebSocket uses a versioned envelope with a process `epoch` and a
monotonic `sequence`. State-changing events are kept in a bounded in-memory
replay ring. Token deltas and command-output chunks remain non-replayable
display aids; their final REST/store records are authoritative. A reconnecting
client supplies its last epoch and sequence. The server either replays the
available state events or sends `resync_required`, after which the client
hydrates the existing REST snapshots.

## Gateway transaction model

All gateway mutations are serialized. A mutation builds a candidate policy
snapshot, renders a complete ruleset including live dynamic rules and their
remaining TTL, validates it, and applies it as one nftables batch. Policy state
is committed only after kernel application succeeds. Add, remove, and egress
mode changes therefore cannot report a policy state that nftables did not
accept.

The controller exposes an enforcement status containing the applied
generation, synchronization state, and last apply error. A failed candidate
leaves the previously enforced state active and returns an error. A detected
kernel drift makes new scope decisions fail closed until reconciliation.
Audit failure after enforcement does not trigger a risky compensating network
mutation; it marks audit synchronization degraded and is surfaced separately.

## Agentic runtime contract

`TaskRunner` exposes a backend capability document rather than implying that
Graph and Eino have identical mechanics. Graph reports durable graph-checkpoint
resume and per-step memory refresh. Eino reports native tool calling and a
reconstructive restart strategy. Eino creates durable initial task state,
resumes recovered top-level tasks with an explicit side-effect warning, and
propagates approval-mode changes to specialist runners.

Delegated work remains depth-one and concurrency-bounded. Parent/child task
relationships are durable and the API/UI retain terminal child snapshots, so
completed or interrupted specialists do not disappear. Capability metadata
also reports the configured roles, retrieval strategy, loaded skills, and
whether skill curation is available. Memory content remains locally stored;
raw target-controlled observations stay excluded from wire payloads.

## Operator cockpit

The existing phosphor/teal visual identity remains. The signature element is a
compact trust rail joining four facts that operators otherwise have to infer:
event connection/recovery state, active agent backend and model capability,
gateway desired/enforced synchronization, and current parent/sub-agent tree.

The gateway screen never updates a security mode optimistically. It displays
the server-confirmed enforced mode, generation, and reconciliation status.
The task card shows all live and terminal specialist children, their role and
status, and keeps approval controls attached to the exact child. TRACE exposes
memory strategy and skill governance without rendering raw recalled content.
All new copy is added to both build-time dictionaries. Controls have visible
focus, status is not color-only, live regions are bounded, and reduced-motion
preferences are respected.

## Error and recovery semantics

- `turn_in_progress`: no history mutation; UI keeps the operator's draft.
- WebSocket replay gap or epoch change: UI enters recovering state and fetches
  authoritative snapshots.
- nftables validation/application failure: no candidate policy commit and no
  successful API response.
- enforcement drift: scope checks fail closed and the UI shows a critical
  reconciliation state.
- restart during a top-level task: Graph resumes from its checkpoint; Eino
  resumes reconstructively from durable task/activity/execution state and is
  told to verify before repeating side effects.
- interrupted child task: it remains visible as a terminal child; the parent
  reconciles it rather than automatically duplicating the side effect.

## Verification

Each slice follows red-green TDD. Required final evidence is root Go tests,
orchestrator Go tests (including race-sensitive packages), Rust tests, UI
tests, TypeScript build, production UI build, and `git diff --check`. Live
Docker validation additionally checks a reconnect/replay, a failed gateway
mutation leaving the old mode enforced, and a recovered task when the local
stack is available.

