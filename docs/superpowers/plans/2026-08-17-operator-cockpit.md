# Operator Cockpit Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Present control-plane, gateway, agent tree, memory, and skill state in one accessible operator cockpit.

**Architecture:** Add a compact trust rail to the existing assistant surface and enrich Gateway/TRACE from authoritative capability and enforcement endpoints.

**Tech Stack:** React 18, TypeScript, CSS, Vitest

**Spec:** `docs/superpowers/specs/2026-08-17-agent-control-plane-program-design.md`

## Global Constraints

- Preserve the current phosphor/teal identity and build-time EN/TR dictionary.
- Never display an optimistic network enforcement state.
- Status must not rely on color alone; reduced motion and keyboard focus are required.

---

### Task 1: Trust rail and complete task tree

**Files:**
- Create: `ui/src/features/controlplane/TrustRail.tsx`
- Create: `ui/src/features/controlplane/TrustRail.css`
- Create: `ui/src/features/controlplane/useAgentCapabilities.ts`
- Modify: `ui/src/features/assistant/AssistantPanel.tsx`
- Modify: `ui/src/features/tasks/TaskTracker.tsx`
- Test: `ui/src/features/tasks/TaskTracker.test.tsx`

**Interfaces:**
- Consumes: WebSocket connection state and `/api/v1/agent/capabilities`.
- Produces: accessible trust rail and retained parent/child task tree.

- [ ] Write UI tests for recovering/stale status and concurrent terminal/live children.
- [ ] Run Vitest and observe the missing UI failures.
- [ ] Implement the rail and retain child snapshots rather than deleting terminal children.
- [ ] Add explicit labels, focus states, and reduced-motion behavior.
- [ ] Run focused UI tests until green.

### Task 2: Gateway enforcement and TRACE governance

**Files:**
- Modify: `ui/src/features/gateway/useGatewayData.ts`
- Modify: `ui/src/pages/gateway/GatewayPage.tsx`
- Modify: `ui/src/pages/trace/TracePage.tsx`
- Modify: `ui/src/features/trace/useTraceData.ts`
- Modify: `ui/src/entities/types.ts`
- Modify: `ui/src/shared/i18n/strings.ts`
- Test: `ui/src/features/gateway/useGatewayData.test.ts`
- Test: `ui/src/shared/i18n/strings.test.ts`

**Interfaces:**
- Consumes: gateway enforcement and agent capability responses.

- [ ] Write tests proving mode changes wait for confirmed enforcement and governance metadata renders safely.
- [ ] Run focused tests and observe failures.
- [ ] Remove optimistic gateway mode state and render enforced generation/sync errors.
- [ ] Add memory strategy, roles, and skill governance summaries to TRACE.
- [ ] Run UI tests, TypeScript build, and production build until green.

