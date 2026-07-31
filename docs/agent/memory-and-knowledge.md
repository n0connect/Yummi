← [Back to README](../../README.md) · [Docs index](../README.md) · [Agent overview](overview.md)

# Memory & The Knowledge Store

`orchestrator/internal/knowledge` is the single owner of Yummi's world model: targets, findings, procedural tactics, persistent context, and embeddings. Agent logic and API handlers share this store; the executor remains concerned with command/network execution rather than world-model records.

## Table of contents

- [Four kinds of memory, one store](#four-kinds)
- [Why procedural memory is target-scrubbed](#target-scrubbing)
- [Retrieval: structured today, semantic also today](#retrieval)
- [How retrieval actually reaches the prompt](#reaching-the-prompt)
- [Persistent context — the "never forget" layer](#persistent-context)

---

## Four kinds of memory, one store <a id="four-kinds"></a>

| Type | Go struct | Carries a target/session value? | Lifetime |
|---|---|---|---|
| **Target** | `Target` | Yes — it *is* a host/IP | Session-scoped |
| **Finding** | `Finding` | Via `TargetID` (optional) | Session-scoped, factual |
| **Tactic** | `Tactic` | **No** — deliberately scrubbed, see below | Cross-session, procedural |
| **PersistentContext** | `PersistentContext` | Yes — session-scoped | Session-scoped, but survives a task's own sliding window / context compaction |

The factual layer (**Target**, **Finding**) answers "what do we know about *this* engagement" — it's exactly as specific as an engagement needs to be, including the target's own IP or hostname. The procedural layer (**Tactic**) answers a completely different question — "what command *shapes* tend to work for this class of tool" — and is designed from the ground up to generalize across engagements, which is precisely why it must never carry a real target value (see [below](#target-scrubbing)). **PersistentContext** sits in between: it's session-scoped like a Finding, but unlike a Finding it's not a discrete fact — it's the kind of thing that needs to persist verbatim across a long task's own context compaction, like a source-code connection map the agent built up over many steps and would be expensive to reconstruct from scratch. It is written exclusively through the `REMEMBER` control tag and the `remember` graph node (see [Graph & Nodes](graph-and-nodes.md#the-nodes)) — there's no other write path into it.

## Why procedural memory is target-scrubbed <a id="target-scrubbing"></a>

A `Tactic`'s `CommandPattern` field replaces the actual target with a literal `<TARGET>` placeholder before it's ever written to disk (`scrubTarget`, called from both retrieval strategies and the recording path). This isn't an incidental implementation detail — it's the entire reason procedural memory is allowed to be **cross-session** at all. A tactic recorded as `nmap -sV <TARGET>` → `worked` is safe to recall on a completely different engagement next month; a tactic recorded as `nmap -sV 10.4.4.12` → `worked` would be leaking one client's IP into the reasoning context of an unrelated future engagement, which is exactly the kind of scope bleed the rest of this system (the gateway, the fail-closed network model) works hard to prevent at the network layer. Memory design has to honor the same boundary the network layer does, or it becomes the leak the network layer was built to close.

`Tactic.HitCount` and `LastUsedAt` accumulate every time the same scrubbed pattern is recorded again — a command shape that's worked ten times across ten different engagements is a stronger signal than one that's worked once, and the retrieval strategies below weight outcomes accordingly (a `worked` tactic ranks above a `failed` one at the same similarity score — see `outcomeWeight` in `embeddings.go`).

## Retrieval: structured today, semantic also today <a id="retrieval"></a>

**Both retrieval strategies described here are implemented and working today** — this is worth stating plainly because some older planning notes describe semantic retrieval as a future roadmap item; the code has since caught up, and this page reflects the actual current state, not the plan that preceded it.

`RetrievalStrategy` (`retrieval.go`) is a one-method interface — `Retrieve(ctx, Query) ([]MemoryItem, error)` — with two implementations:

- **`StructuredRetrieval`** — always recalls bounded, session-scoped `PersistentContext` ranked by goal-keyword overlap and recency, then recent findings, plus tactics matching tools actually used in the task. It is deterministic and has zero model dependency.
- **`SemanticRetrieval`** — embeds `goal + tools`, ranks persistent contexts, findings, and cross-tool tactics by cosine similarity, and falls back per category to structured/recency recall for older rows without embeddings. A `masscan` tactic can therefore surface for an `nmap`-oriented goal when their embeddings are close enough.

**The storage is plain SQLite, not a vector-index extension.** Embeddings live in an ordinary `embeddings` table (`source_table`, `source_id`, `model`, `dim`, a serialized `vector` BLOB). `NearestTactics`, `NearestFindings`, and `NearestContexts` pull a bounded candidate set and compute cosine similarity in Go. The current per-query candidate ceiling is appropriate for the single-operator local product; move to an ANN extension before treating the store as a multi-tenant, high-volume service.

**Degradation is total and silent to the agent loop.** `SemanticRetrieval` falls back to `StructuredRetrieval` (never returns an error upward) whenever: no embedder is configured, the embed call itself fails, or the nearest-neighbor lookup errors. Concretely, this means if you haven't pulled the embedding model (`nomic-embed-text`, `make install`'s optional second pull — see [Installation § Ollama setup](../installation.md#ollama-setup)) or it later becomes unreachable, the agent doesn't lose memory retrieval entirely — it just quietly loses the *cross-tool* generalization and keeps exact-match recall.

## How retrieval actually reaches the prompt <a id="reaching-the-prompt"></a>

A `Query` deliberately excludes commands already run in the **current** task (`ExcludeCommands`, matched via the same `scrubTarget` pattern) from its results. This is a specific, tested fix for a specific failure mode: without it, procedural memory could hand the model back a tactic that is literally the command it just ran two steps ago in the same task, re-injecting it into context as if it were new advice — and a weak model re-reading "this command worked" right after having just run that exact command is a documented way to nudge it into a repeat-command loop. Procedural memory exists for **cross-task transfer** (what worked last time, in a different task) — the current task's own working set is already in its observation history and doesn't need memory to remind it.

## Persistent context — the "never forget" layer <a id="persistent-context"></a>

`PersistentContext.Origin` is provenance. The graph records `agent` or `subagent`; `operator` remains available for future write APIs. `REMEMBER` writes are ANSI-stripped and scanned at `ScopeStrict`, bounded to 64 KiB each, upserted by `(session,key)`, and capped at 200 entries per session. Both retrieval strategies inject them into later task prompts — including the first step after restart. Operators can explicitly forget an entry with `DELETE /sessions/{sessionID}/context/{key}`, which also removes its embedding.
