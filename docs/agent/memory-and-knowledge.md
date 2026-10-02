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
| **Target** | `Target` | Explicit IP / Hostname | Session-scoped | In-scope boundary declared by operator or discovered. |
| **Finding** | `Finding` | Linked via `TargetID` | Session-scoped | Verified vulnerability with severity, evidence, and PoC. |
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
4. **Report Gate**: The multi-format report engine (`internal/report`) queries `ClassifyScope` for every asset and finding; out-of-scope items are automatically excluded from final deliverables.
5. **Live Classifier API**: Operators and UI panels can evaluate any hostname or URL against active rules via `POST /api/v1/sessions/{sessionID}/scope-rules/classify`.

---

## Target-Scrubbing & Procedural Memory <a id="target-scrubbing"></a>

Procedural tactics record what tool invocations succeed or fail. To prevent cross-client data leakage:

```text
Raw Command:          nmap -sV -p 443 192.168.1.50
Target Scrubbing:     nmap -sV -p 443 <TARGET>
Storage on Disk:      Tactic{CommandPattern: "nmap -sV -p 443 <TARGET>", Outcome: "worked"}
```

Before saving to the `tactics` table, `scrubTarget()` converts all concrete IP addresses, CIDRs, and hostnames to the `<TARGET>` token. A tactic recorded on client A's engagement can safely assist the agent on client B's engagement without revealing client A's infrastructure.

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
   - Recency & frequency sorting                 - SQLite Cosine Similarity calculation
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
    source_table TEXT NOT NULL,
    source_id    TEXT NOT NULL,
    model        TEXT NOT NULL,
    dim          INTEGER NOT NULL,
    vector       BLOB NOT NULL,
    PRIMARY KEY (source_table, source_id)
);
```

### Mathematical Scoring:
The similarity score between goal embedding $\mathbf{u}$ and stored tactic embedding $\mathbf{v}$ is computed in Go via cosine similarity:

$$\text{sim}(\mathbf{u}, \mathbf{v}) = \frac{\mathbf{u} \cdot \mathbf{v}}{\|\mathbf{u}\| \|\mathbf{v}\|} = \frac{\sum_{i=1}^d u_i v_i}{\sqrt{\sum_{i=1}^d u_i^2} \sqrt{\sum_{i=1}^d v_i^2}}$$

When ranking tactics, the system applies an outcome multiplier (successful tactics are weighted higher than failed ones at equivalent similarity).

---

## Persistent Context: The Never-Forget Layer <a id="persistent-context"></a>

`PersistentContext` entries are written via the `REMEMBER` control tag during task execution:
- Maximum size per entry: 64 KiB.
- Capped at 200 entries per session.
- Scanned at `ScopeStrict` to prevent prompt injection persistence.
- Survives task context compaction and is re-injected at the beginning of subsequent tasks.
