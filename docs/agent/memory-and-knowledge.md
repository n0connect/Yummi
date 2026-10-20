[Back to Project README](../../README.md) · [Docs index](../README.md) · [Agent overview](overview.md)

# Memory & The Knowledge Store

`orchestrator/internal/knowledge` owns the agent's world model: targets, security findings, procedural tactics, persistent context, and the Attack Surface Management (ASM) asset matrix.

---

## Table of Contents

- [The Six Kinds of Structured Knowledge](#six-kinds)
- [Attack Surface Management (ASM) & Asset Hierarchy](#asm-matrix)
- [Program Scope Rules & Bug Bounty Bookkeeping](#program-scope)
- [Target-Scrubbing & Procedural Memory](#target-scrubbing)
- [Retrieval Architecture: Structured vs Semantic](#retrieval)
- [SQLite Vector Embeddings & Cosine Ranking](#embeddings)
- [Persistent Context: The Never-Forget Layer](#persistent-context)

---

## The Six Kinds of Structured Knowledge <a id="six-kinds"></a>

```text
+-----------------------------------------------------------------------------------+
|                              KNOWLEDGE STORE MEMORY LAYERS                        |
|                                                                                   |
|   +--------------------------+  +--------------------------+  +-----------------+ |
|   |         Target           |  |         Finding          |  |    AssetNode    | |
|   | (Declared / In-Scope IP) |  | (Structured Vulnerability|  | (Hierarchical   | |
|   | Scope: Session           |  |  with Evidence PoC)      |  |  Attack Surface)| |
|   +--------------------------+  +--------------------------+  +-----------------+ |
|                                                                                   |
|   +--------------------------+  +--------------------------+  +-----------------+ |
|   |        ScopeRule         |  |    PersistentContext     |  |     Tactic      | |
|   | (Bug-Bounty Include /    |  | (Session factual memory, |  | (Cross-session  | |
|   |  Exclude direct policy)  |  |  compact-resilient)      |  |  target-scrub)  | |
|   +--------------------------+  +--------------------------+  +-----------------+ |
+-----------------------------------------------------------------------------------+
```

| Type | Go Struct | Target / Scope Value | Lifetime | Description |
|---|---|---|---|---|
| **Target** | `Target` | Explicit IP / Hostname | Session-scoped | Declared/discovered target record; not a gateway allow rule. |
| **Finding** | `Finding` | Linked via `TargetID` | Session-scoped | Recorded security claim, severity, evidence and optional PoC; storage alone does not verify exploitation. |
| **AssetNode** | `AssetNode` | Domain / Endpoint / IP | Session-scoped | Node in hierarchical attack surface tree. |
| **ScopeRule** | `ScopeRule` | Hostname glob pattern | Session-scoped | Bug bounty include/exclude directives for report gating. |
| **PersistentContext** | `PersistentContext` | Session-specific | Session-scoped | Contextual facts persisted across prompt compaction. |
| **Tactic** | `Tactic` | **No** (Strictly scrubbed) | Cross-session | Command patterns and tool behaviors that succeeded across tasks. |

---

## Attack Surface Management (ASM) & Asset Hierarchy <a id="asm-matrix"></a>

The Knowledge Store organizes discovered assets into a structured hierarchical tree:

```text
AssetDomain (example.com)
  |
  |-- AssetSubdomain (api.example.com)
  |     |
  |     |-- AssetIP (10.0.0.5)
  |     |     |
  |     |     \-- AssetPort (443/TCP - HTTPS)
  |     |
  |     \-- AssetEndpoint (GET /v1/users)
  |           |
  |           |-- AssetParameter (id)
  |           \-- AssetParameter (filter)
  |
  \-- AssetSubdomain (vault.example.com)
        |
        \-- AssetCloudBucket (s3://vault-backups-example)
```

### Supported Asset Types (`AssetType`):
- `domain`, `subdomain`, `ip`, `port`, `endpoint`, `parameter`, `technology`, `cloud_bucket`, `secret`.

### API Retrieval:
- `GET /api/v1/sessions/{sessionID}/assets?format=tree` -> Returns nested JSON hierarchy.
- `GET /api/v1/sessions/{sessionID}/assets?format=flat` -> Returns flat asset list with parent pointers.

---

## Program Scope Rules & Bug Bounty Bookkeeping <a id="program-scope"></a>

A bug bounty engagement's published rules of engagement define which domains are in-scope and which are out-of-scope. Submitting findings on out-of-scope assets is a serious violation. `orchestrator/internal/knowledge/program_scope.go` provides dedicated bookkeeping for this policy:

```go
type ScopeRule struct {
    ID        string        `json:"id"`
    SessionID string        `json:"session_id"`
    Pattern   string        `json:"pattern"` // e.g. "example.com" or "*.example.com"
    Kind      ScopeRuleKind `json:"kind"`    // "include" or "exclude"
    Note      string        `json:"note,omitempty"`
    CreatedAt int64         `json:"created_at"`
}
```

### Key Properties & Invariants:
1. **Knowledge-Layer Only**: Program Scope Rules do not modify `nftables` or network packets (that remains the sole purview of the Rust gateway daemon). Instead, they classify assets and safeguard reporting.
2. **Wildcard & Subdomain Matching**: `*.example.com` matches the apex domain (`example.com`) as well as any subdomain at any depth (`api.example.com`, `v1.dev.example.com`).
3. **Precedence**: `exclude` rules take strict precedence over `include` rules.
4. **Report Scope Classification & Warnings**: The report engine (`internal/report`) queries `ClassifyScope` for discovered host assets and renders in-scope/out-of-scope status in the report table, printing a prominent warning if out-of-scope hosts are detected. It does not automatically filter them out or modify network permissions.
5. **Live Classifier API**: Operators and UI panels can evaluate any hostname or URL against active rules via `POST /api/v1/sessions/{sessionID}/scope-rules/classify`.

---

## Target-Scrubbing & Procedural Memory <a id="target-scrubbing"></a>

Procedural tactics record what tool invocations succeed or fail. To prevent cross-client data leakage:

```text
Raw Command:          nmap -sV -p 443 192.168.1.50
Target Scrubbing:     nmap -sV -p 443 <TARGET>
Storage on Disk:      Tactic{CommandPattern: "nmap -sV -p 443 <TARGET>", Outcome: "worked"}
```

Before saving procedural memory, `scrubTarget()` replaces occurrences of the target returned by `scope.ExtractTarget` with `<TARGET>`; it skips a command if that extracted token cannot be removed. This is a single extracted-target scrub, not comprehensive anonymization of every address, secret, file path or argument in a compound command. Foreign template placeholders and threat patterns are rejected. Reuse across sessions must not be described as a blanket guarantee against engagement-data leakage. See `knowledge/procedural.go`.

---

## Retrieval Architecture: Structured vs Semantic <a id="retrieval"></a>

```text
                               Agent Task Goal + Tools
                                          |
                                          v
                         +--------------------------------+
                         |      RetrievalStrategy         |
                         +----------------+---------------+
                                          |
                   +----------------------+----------------------+
                   |                                             |
        [StructuredRetrieval]                         [SemanticRetrieval]
                   |                                             |
   - Keyword overlap matching                    - Ollama Embedding (`nomic-embed-text`)
   - Recency & frequency sorting                 - Go cosine-similarity calculation over SQLite-stored vectors
   - Zero model dependency (deterministic)       - Falls back gracefully to Structured
                   |                                             |
                   +----------------------+----------------------+
                                          |
                                          v
                   Rendered into Prompt: MEMORY (relevant tactics & facts)
```

---

## SQLite Vector Embeddings & Cosine Ranking <a id="embeddings"></a>

Embeddings are stored directly in SQLite:
```sql
CREATE TABLE embeddings (
    id TEXT PRIMARY KEY,
    source_table TEXT NOT NULL,
    source_id    TEXT NOT NULL,
    model        TEXT NOT NULL,
    dim          INTEGER NOT NULL,
    vector       BLOB NOT NULL,
    created_at   INTEGER NOT NULL
);
CREATE UNIQUE INDEX idx_embeddings_source
    ON embeddings(source_table, source_id, model);
```

### Mathematical Scoring:
The similarity score between goal embedding $\mathbf{u}$ and stored tactic embedding $\mathbf{v}$ is computed in Go via cosine similarity:

$$\text{sim}(\mathbf{u}, \mathbf{v}) = \frac{\mathbf{u} \cdot \mathbf{v}}{\|\mathbf{u}\| \|\mathbf{v}\|} = \frac{\sum_{i=1}^d u_i v_i}{\sqrt{\sum_{i=1}^d u_i^2} \sqrt{\sum_{i=1}^d v_i^2}}$$

When ranking tactics, the system applies an outcome multiplier (successful tactics are weighted higher than failed ones at equivalent similarity).

---

## Persistent Context: The Never-Forget Layer <a id="persistent-context"></a>

`PersistentContext` entries are written via the `REMEMBER` control tag during task execution:
- Limits: 200 Unicode code points per key and 65,536 per value (not 64 KiB in UTF-8 bytes).
- Capped at 200 entries per session.
- Scanned at `ScopeStrict` to prevent prompt injection persistence.
- Saves/updates by key; `REMEMBER` with an empty value deletes the note through shared `ApplyNote`.
- Survives compaction/restarts and participates in later session prompt memory within retrieval/output bounds. The 200-note cap rejects new entries until a stale note is forgotten; updates to existing keys remain possible.
