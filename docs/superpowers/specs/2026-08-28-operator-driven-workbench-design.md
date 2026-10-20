# Operator-Driven Workbench — direction change

> **Historical record —** This dated plan/report describes its recorded revision and environment. It is not current implementation or deployment verification. Use the [maintained documentation](../../../docs/README.md) for current behavior. Original findings and results are preserved below.

**Date:** 2026-08-28
**Status:** In progress — first brick landed, rest is designed, not built.

## Why this exists

Yummi drifted into "AI drives, operator approves." Every capability built —
skills, tactics, persistent memory, planner, sub-agents, threat scan,
persona — feeds the AI conductor. The human's own surface is either the
Agent tab (ask the AI, approve its proposals) or the Bash tab (a bare
`docker exec` shell, cut off from everything Yummi has accumulated).

That is the wrong shape for the actual user: an expert red teamer / web /
reverse-engineering operator who wants **their own environment**, where the
AI is a co-pilot they command — not a conductor that commands them.

The fix is not a feature. It is an inversion of who is in the driver's seat.

## The two products hiding inside Yummi today

1. **"AI does it, I approve"** — the Agent tab. All the engineering lives here.
2. **"I do it, alone"** — the Bash tab. Naked shell, zero augmentation.

The operator wants a third thing that does not exist yet:

3. **"I do it, the AI's accumulated intelligence serves me while I work."**

## What "operator-driven" concretely means

The operator stays in control. The AI's role flips from actor to advisor +
record-keeper. Building blocks, roughly in dependency order:

- **[DONE] Operator briefing** (`GET /api/v1/operator/briefing`). Ask Yummi
  what it already knows about a tool/goal — matching skills, tactics that
  worked, findings on record — surfaced for the human's own use, not folded
  into an agent prompt. `handlers_operator.go`.
- **[NEXT] Operator-recorded findings from manual work.** The Bash terminal
  (or a richer workbench surface) lets the operator save a finding by their
  own hand from what they just saw — `POST /sessions/{id}/findings` already
  exists, it just isn't wired to the manual-work surface. Their manual
  discoveries join the same knowledge store the AI's do.
- **[NEXT] Tactic capture from manual commands.** A command the operator ran
  by hand that worked is exactly the procedural memory the store is built
  for — offer to record it (target-scrubbed, same as the agent path) so the
  operator's own muscle memory compounds across engagements.
- **[LATER] Inline co-pilot in the terminal.** As the operator types, surface
  the matching skill/tactic beside the prompt (advisory, never auto-run).
  This is the real "co-pilot you command" moment. The terminal is a raw PTY
  WS bridge today (`terminal.go`); doing this well needs a companion side
  panel driven by the briefing endpoint, not PTY parsing.
- **[LATER] Ask-the-AI-a-question, mid-flow, without handing over the wheel.**
  A one-shot "given what I'm looking at, what would you check next?" that
  returns advice as text — the operator decides, the AI never executes.

## The separate-sandbox question (unresolved, operator's original design)

The operator originally designed separate sandbox environments — one for
red-team/pentest, one for reverse engineering — because RE routinely means
running an untrusted binary, which should not share an environment with
live-target pentest work. Today both collapse into the single Kali sandbox,
separated only by sub-agent role/prompt, not by environment isolation. The
`reverser` sub-agent and `reverse-engineering` skill still exist and work,
but the environment-level separation the operator envisioned does not.

Open decision: is role-level separation an acceptable trade-off, or does RE
warrant its own isolated sandbox (a real second environment, so a detonated
binary can never touch pentest state or reach the network the way a scan
can)? This is a genuine architecture fork, not settled. It should be
decided deliberately, with the operator, not defaulted.

## What this direction does NOT change

- The trust boundary stays. Operator-driven does not mean operator-bypasses-
  the-gateway — the Bash path already crosses gateway's nftables policy, and
  anything new here must too. Fail-closed is untouched.
- The AI backend (graph/eino/brain) stays. This is a new *surface* over the
  same substrate, not a replacement for the conductor — an operator who
  wants the AI to drive can still let it.

## Honest note

Model quality is still the ceiling for the AI-driven path (proven live this
session). The operator-driven direction sidesteps that entirely: the human
is the intelligence, and Yummi's job is to make their accumulated knowledge
and its fail-closed substrate serve them. That is the part no competitor's
LLM wrapper can copy, and the part that makes this project the operator's
own again.
