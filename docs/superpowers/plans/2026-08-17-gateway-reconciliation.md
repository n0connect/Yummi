# Gateway Reconciliation Implementation Plan

> **Historical record —** This dated plan/report describes its recorded revision and environment. It is not current implementation or deployment verification. Use the [maintained documentation](../../../docs/README.md) for current behavior. Original findings and results are preserved below.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep Rust policy state and nftables enforcement transactionally consistent.

**Architecture:** Render candidate policy into one checked nftables batch, apply before policy commit, and expose enforced generation/health over IPC and REST.

**Tech Stack:** Rust, nftables, Unix IPC JSON, Go REST bridge

**Spec:** `docs/superpowers/specs/2026-08-17-agent-control-plane-program-design.md`

## Global Constraints

- Gateway remains the sole scope authority.
- Mutation failure preserves the previously enforced state.
- No separate delete-table command may create an unfiltered window.

---

### Task 1: Atomic ruleset application

**Files:**
- Modify: `gateway/src/network/nftables.rs`
- Test: `gateway/src/network/nftables.rs`

**Interfaces:**
- Produces: checked single-batch ruleset application and a generation status.

- [ ] Add tests showing replacement is one batch and candidate dynamic rules survive a mode rebuild.
- [ ] Run focused Cargo tests and observe the expected failures.
- [ ] Parameterize ruleset rendering by candidate mode/rules and remove the separate table deletion.
- [ ] Preflight then apply the same script, committing controller metadata only after success.
- [ ] Run focused Cargo tests until green.

### Task 2: Policy/kernel commit ordering

**Files:**
- Modify: `gateway/src/policy/mod.rs`
- Modify: `gateway/src/ipc/mod.rs`
- Modify: `gateway/src/ipc/protocol.rs`
- Modify: `orchestrator/internal/gateway/protocol.go`
- Modify: `orchestrator/internal/gateway/client.go`
- Modify: `orchestrator/internal/api/handlers_gateway.go`

**Interfaces:**
- Produces: candidate rule preparation/commit and enforcement status on status responses.

- [ ] Add Rust IPC tests for failed add/remove/mode mutations leaving policy unchanged.
- [ ] Run focused tests and observe the old policy-first behavior fail them.
- [ ] Serialize mutations, apply candidate ruleset, then commit the exact candidate policy object.
- [ ] Thread enforcement status through Go IPC and REST types.
- [ ] Run Rust and Go gateway tests until green.

