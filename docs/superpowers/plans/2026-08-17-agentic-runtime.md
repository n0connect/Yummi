# Agentic Runtime Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make backend differences explicit and close Eino lifecycle, delegation, memory, and skill-governance gaps.

**Architecture:** Extend `TaskRunner` with a wire-safe capability document, add reconstructive Eino restart, propagate governance settings to specialists, and retain durable child lifecycle evidence.

**Tech Stack:** Go, SQLite, Eino ADK, Ollama, local knowledge/skill stores

**Spec:** `docs/superpowers/specs/2026-08-17-agent-control-plane-program-design.md`

## Global Constraints

- Graph remains the small-model state-machine backend; Eino remains native tool calling.
- Raw observations and model rationale never become wire-visible.
- Sub-agent depth stays one and concurrency remains bounded.

---

### Task 1: Capability and recovery contract

**Files:**
- Modify: `orchestrator/internal/agent/task_runner.go`
- Modify: `orchestrator/internal/agent/runner.go`
- Modify: `orchestrator/internal/agent/eino/runner.go`
- Modify: `orchestrator/internal/api/tasks.go`
- Modify: `orchestrator/internal/api/routes.go`
- Test: `orchestrator/internal/agent/eino/runner_test.go`
- Test: `orchestrator/internal/api/handlers_test.go`

**Interfaces:**
- Produces: `Capabilities() BackendCapabilities` and `GET /api/v1/agent/capabilities`.

- [ ] Write tests for honest Graph/Eino capabilities, Eino recovered-task resume, and approval-mode propagation.
- [ ] Run focused tests and observe the capability/recovery failures.
- [ ] Add capability documents and reconstructive Eino recovery with side-effect warning.
- [ ] Propagate mode changes to every specialist runner.
- [ ] Run agent and API tests until green.

### Task 2: Durable specialist visibility and governance telemetry

**Files:**
- Modify: `orchestrator/internal/agent/task.go`
- Modify: `orchestrator/internal/agent/store.go`
- Modify: `orchestrator/internal/agent/delegation.go`
- Modify: `orchestrator/internal/agent/promptblocks.go`
- Test: `orchestrator/internal/agent/delegation_test.go`
- Test: `orchestrator/internal/agent/store_test.go`

**Interfaces:**
- Produces: wire-safe child role/status metadata and retrieval/skill capability counts.

- [ ] Add persistence tests for child role and terminal visibility.
- [ ] Run focused tests and observe the missing metadata failure.
- [ ] Persist role metadata without exposing raw observations.
- [ ] Report retrieval strategy and skill governance availability in capabilities.
- [ ] Run agent tests until green.

