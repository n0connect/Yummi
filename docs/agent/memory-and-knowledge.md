← [Back to README](../../README.md) · [Docs index](../README.md) · [Agent overview](overview.md)

# Memory & The Knowledge Store

`orchestrator/internal/knowledge` is, by its own doc comment, "the single owner of Yummi's world model" — every target, finding, and piece of procedural/persistent memory in the system lives here, in one SQLite-backed store, so the agent, the executor, and the API are all reading and writing the *same* world model rather than three components maintaining their own private copies that could drift apart.

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

- **`StructuredRetrieval`** — exact tool-name match. Given a step's relevant tools (e.g. `["nmap"]`), it pulls tactics recorded under exactly that tool name via plain SQL. Simple, deterministic, zero extra dependencies — but it has a real gap: a tactic recorded under `masscan` will never surface for a query about `nmap`, even though both are port scanners and the tactic might be just as relevant.
- **`SemanticRetrieval`** — embeds the query's tool list via Ollama (any model implementing the small `Embedder` interface — `Embed(ctx, text) ([]float32, error)`; `*llm.Client` satisfies this structurally, no import cycle needed) and ranks tactics by **cosine similarity** against embeddings saved when each tactic was originally recorded. This is what closes the `StructuredRetrieval` gap above — a `masscan` tactic can now surface for an `nmap`-tagged query if their embeddings are close enough, because the ranking is over meaning, not over an exact string match.

**The storage is plain SQLite, not a vector-index extension.** Embeddings live in an ordinary `embeddings` table (`source_table`, `source_id`, `model`, `dim`, a serialized `vector` BLOB) — `NearestTactics`/`NearestFindings` (`embeddings.go`) pull every candidate row for the current embedding model and compute cosine similarity in Go application code, not via a SQL extension like `sqlite-vec`. This is a deliberate simplicity choice appropriate to the data volumes a single operator's own engagements actually produce — a brute-force scan over a few hundred or thousand tactic rows is fast enough that a specialized vector index would be solving a scale problem this system doesn't have.

**Degradation is total and silent to the agent loop.** `SemanticRetrieval` falls back to `StructuredRetrieval` (never returns an error upward) whenever: no embedder is configured, the embed call itself fails, or the nearest-neighbor lookup errors. Concretely, this means if you haven't pulled the embedding model (`nomic-embed-text`, `make install`'s optional second pull — see [Installation § Ollama setup](../installation.md#ollama-setup)) or it later becomes unreachable, the agent doesn't lose memory retrieval entirely — it just quietly loses the *cross-tool* generalization and keeps exact-match recall.

## How retrieval actually reaches the prompt <a id="reaching-the-prompt"></a>

A `Query` deliberately excludes commands already run in the **current** task (`ExcludeCommands`, matched via the same `scrubTarget` pattern) from its results. This is a specific, tested fix for a specific failure mode: without it, procedural memory could hand the model back a tactic that is literally the command it just ran two steps ago in the same task, re-injecting it into context as if it were new advice — and a weak model re-reading "this command worked" right after having just run that exact command is a documented way to nudge it into a repeat-command loop. Procedural memory exists for **cross-task transfer** (what worked last time, in a different task) — the current task's own working set is already in its observation history and doesn't need memory to remind it.

## Persistent context — the "never forget" layer <a id="persistent-context"></a>

`PersistentContext.Origin` (`"agent"` / `"subagent"` / `"operator"`) is an audit/provenance field, not decoration — it's how you can tell, looking at the Trace tab's Context section later, whether a piece of standing memory was written by the top-level agent, by a delegated sub-agent (see [Skills & Sub-agents](skills-and-subagents.md)), or entered manually by an operator. Writing to this layer goes through the exact same [content-layer threat scan](../security-model.md#layer-content) as everything else user-mediated in this system — at `ScopeStrict`, block-on-hit, applied to `key+value` *after* ANSI-stripping — because a value here is re-injected into every future prompt for the rest of the session. A poisoned value that made it past a one-time tool-output check would otherwise become a standing, repeating injection for as long as the session lives; the stricter scope and the block-rather-than-warn posture are the direct answer to that specific, higher stakes.
