# Control Plane Events Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Correlate every chat turn and replay missed state events safely.

**Architecture:** Add a per-session turn lease in the conductor and a shared API event hub with epoch/sequence replay. REST remains authoritative for final chat and execution state.

**Tech Stack:** Go, React 18, TypeScript, Gorilla WebSocket, Vitest

**Spec:** `docs/superpowers/specs/2026-08-17-agent-control-plane-program-design.md`

## Global Constraints

- Preserve session isolation and the single sandbox execution path.
- Stream tokens and output chunks are display aids, not durable truth.
- Every new UI string exists in English and Turkish.

---

### Task 1: Turn identity and single-flight history

**Files:**
- Modify: `orchestrator/internal/conductor/stream.go`
- Modify: `orchestrator/internal/conductor/engine.go`
- Modify: `orchestrator/internal/api/handlers_chat.go`
- Modify: `ui/src/features/assistant/AssistantPanel.tsx`
- Modify: `ui/src/entities/types.ts`
- Test: `orchestrator/internal/conductor/stream_test.go`
- Test: `orchestrator/internal/api/chat_test.go`

**Interfaces:**
- Produces: `ChatWithHistoryTurn(ctx, sessionID, turnID, message)` and `turn_id` on stream/REST payloads.

- [ ] Write tests proving concurrent turns in one session return a conflict and different sessions remain concurrent.
- [ ] Run the focused Go tests and observe the expected missing-contract failures.
- [ ] Add the turn lease and echo the validated UUID through stream and REST responses.
- [ ] Add client turn generation and filter deltas by the active turn.
- [ ] Run focused Go/UI tests until green.

### Task 2: Replayable event hub

**Files:**
- Create: `orchestrator/internal/api/event_hub.go`
- Create: `orchestrator/internal/api/event_hub_test.go`
- Modify: `orchestrator/internal/api/handlers_chat.go`
- Modify: `orchestrator/internal/api/routes.go`
- Modify: `ui/src/features/chat/useChatStream.ts`

**Interfaces:**
- Produces: envelope fields `schema_version`, `epoch`, `sequence`; reconnect query `epoch` and `after`.

- [ ] Write hub tests for ordered replay, replay gaps, session filtering, and non-replayable cosmetic events.
- [ ] Run the focused test and observe failure because the hub does not exist.
- [ ] Implement the bounded hub and atomically subscribe after replay capture.
- [ ] Update the browser hook to retain the cursor and request snapshot recovery when required.
- [ ] Run API and UI tests until green.

