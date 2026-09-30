[Back to Project README](../../README.md) · [Docs index](../README.md) · [Agent overview](overview.md)

# Memory & The Knowledge Store

`orchestrator/internal/knowledge` owns the agent's world model: targets, security findings, procedural tactics, persistent context, and the Attack Surface Management (ASM) asset matrix.

---

## Table of Contents

- [The Five Kinds of Memory](#five-kinds)
- [Attack Surface Management (ASM) & Asset Hierarchy](#asm-matrix)
- [Target-Scrubbing & Procedural Memory](#target-scrubbing)
- [Retrieval Architecture: Structured vs Semantic](#retrieval)
- [SQLite Vector Embeddings & Cosine Ranking](#embeddings)
- [Persistent Context: The Never-Forget Layer](#persistent-context)

---

## The Five Kinds of Memory <a id="five-kinds"></a>

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
|   +--------------------------------------+  +-----------------------------------+ |
|   |          PersistentContext           |  |              Tactic               | |
|   | (Session-scoped factual memory,      |  | (Cross-session procedural         | |
|   |  survives context window compaction) |  |  methodology, target-scrubbed)    | |
|   +--------------------------------------+  +-----------------------------------+ |
+-----------------------------------------------------------------------------------+
```

| Type | Go Struct | Target / Scope Value | Lifetime | Description |
|---|---|---|---|---|
| **Target** | `Target` | Explicit IP / Hostname | Session-scoped | In-scope boundary declared by operator or discovered. |
| **Finding** | `Finding` | Linked via `TargetID` | Session-scoped | Verified vulnerability with severity, evidence, and PoC. |
| **AssetNode** | `AssetNode` | Domain / Endpoint / IP | Session-scoped | Node in hierarchical attack surface tree. |
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
