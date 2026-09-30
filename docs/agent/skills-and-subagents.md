[Back to Project README](../../README.md) · [Docs index](../README.md) · [Agent overview](overview.md)

# Skills & Sub-Agents

Two complementary mechanisms extend the agent's problem-solving capabilities without altering its deterministic execution core: **skills** (declarative, trigger-matched methodology injected into prompts) and **sub-agents** (bounded delegation to specialized personas).

---

## Table of Contents

- [Skills: Declarative Offensive Methodology](#skills)
- [Mechanical Skill Selection Algorithm](#skill-selection)
- [The 22 Built-in Red Team Skills](#builtin-skills)
- [Sub-Agents: Bounded Role Delegation](#subagents)
- [The 10 Specialized Sub-Agents](#builtin-subagents)
- [Concurrent Delegation Fan-Out](#concurrent-handoff)
- [Architectural Guard: Depth Capped at 1](#depth-cap)
- [Automated Skill Curator & Sanitization](#auto-skill)

---

## Skills: Declarative Offensive Methodology <a id="skills"></a>

A `Skill` contains knowledge and methodology for the LLM to reason over, not an executable script:

```go
type Skill struct {
    Name        string   `yaml:"name"`
    Description string   `yaml:"description"`
    Triggers    []string `yaml:"triggers"`
    Tools       []string `yaml:"tools"`
    Knowledge   string   `yaml:"knowledge"`
}
```

```text
+-----------------------------------------------------------------------------+
|                          SKILL SELECTION PIPELINE                           |
|                                                                             |
|   Goal String + Recent Tools                                                |
|       |                                                                     |
|       v                                                                     |
|   Registry.Select(goal, tools, max=3)                                       |
|       |                                                                     |
|       +---> Substring Trigger Matching (Deterministic Scoring)              |
|       |                                                                     |
|       v                                                                     |
|   Top Matches Rendered into Step Prompt:                                    |
|   "SKILLS (relevant playbooks — apply the matching approach): ..."          |
+-----------------------------------------------------------------------------+
```

Every skill file is strictly scanned with `threatscan.ScanContent` at `ScopeStrict` upon loading to prevent prompt injection payloads.

---

## The 22 Built-in Red Team Skills <a id="builtin-skills"></a>

| Skill | Category | Key Focus & Methodology | Primary Tools |
|---|---|---|---|
| **`recon`** | Network Recon | Host discovery, port scanning, service versioning | `nmap` |
| **`web`** | Web Application | Web fingerprinting, security headers, WAF triage, directory fuzzing | `whatweb`, `curl`, `gobuster`, `nikto` |
| **`js-recon`** | Web Application | JavaScript endpoint harvesting and route mapping | `katana`, `gau`, `curl` |
| **`parameter-fuzzing`** | Web Application | Query and body parameter fuzzing, hidden debug parameters | `ffuf` |
| **`parameter-discovery`** | Web Application | Deep heuristic parameter discovery across GET, POST, JSON, XML | `arjun`, `ffuf`, `curl` |
| **`secret-mining`** | Web Application | AST parsing of JavaScript bundles for hardcoded secrets and API keys | `jsluice`, `curl`, `katana` |
| **`api-pentest`** | API Security | REST/GraphQL API testing, BOLA/IDOR object enumeration, method tampering | `curl`, `ffuf`, `katana` |
| **`graphql-pentest`** | API Security | GraphQL introspection, query batching, alias overload, depth limits | `graphql_cop`, `curl`, `ffuf` |
| **`auth-logic`** | Identity / Auth | JWT tampering (none alg, key confusion), OAuth2 redirection, session audit | `jwt_tool`, `curl` |
| **`cloud-recon`** | Cloud / ASM | S3/GCS/Azure storage bucket discovery, subdomain takeover auditing | `cloud_enum`, `subzy`, `tlsx` |
| **`sql-injection`** | Injection | Error-based, boolean-blind, and time-based SQL injection validation | `sqlmap`, `curl` |
| **`wordpress-assessment`** | CMS Assessment | WordPress plugin/theme enumeration, known vulnerability verification | `wpscan` |
| **`vuln-scanning`** | Vulnerability Scan | Template-based automated vulnerability detection | `nuclei`, `nikto` |
| **`osint-enum`** | OSINT / Recon | Passive reconnaissance, subdomain discovery, certificate transparency | `subfinder`, `amass`, `theHarvester` |
| **`credential-attack`** | Authentication | Password brute-forcing, service authentication testing | `hydra`, `medusa` |
| **`smb-enum`** | Network / OS | SMB share enumeration, null sessions, user listing | `enum4linux`, `smbclient` |
| **`snmp-enum`** | Network / OS | SNMP community string enumeration, MIB tree walking | `onesixtyone`, `snmpwalk` |
| **`reverse-engineering`** | Binary Analysis | Binary header analysis, exploit mitigations, disassembly triage | `checksec`, `radare2` |
| **`source-analysis`** | Code Review | Static analysis of source trees, dangerous function pattern matching | `grep`, `find` |
| **`system-inspection`** | Linux Privilege | Privilege escalation triage, SUID/GUID binaries, cron job auditing | `find`, `ls` |
| **`software-presence`** | Host Inspection | Installed package and binary presence audit | `dpkg`, `which` |
| **`largest-files`** | Disk Triage | Large backup files, log dump inspection, disk space mapping | `find`, `du` |

---

## Sub-Agents: Bounded Role Delegation <a id="subagents"></a>

A `SubAgentDef` defines an autonomous specialist. At runtime, each becomes a standalone `agent.Runner` running the same graph state machine, scoped to specific skills and sharing the same Knowledge Store.

```text
                               +-----------------------------+
                               |     PARENT AGENT TASK       |
                               +--------------+--------------+
                                              |
                                     [HANDOFF] Control Tag
                                              |
                     +------------------------+------------------------+
                     |                                                 |
         +-----------v------------+                       +------------v-----------+
         | SUB-AGENT: api-hunter  |                       |  SUB-AGENT: js-miner   |
         | (Graph State Machine)  |                       | (Graph State Machine)  |
         +-----------+------------+                       +------------+-----------+
                     |                                                 |
                     +------------------------+------------------------+
                                              |
                                              v
                              +-------------------------------+
                              |    SHARED KNOWLEDGE STORE     |
                              |  (Findings, Assets, Memory)   |
                              +-------------------------------+
```

---

## The 10 Specialized Sub-Agents <a id="builtin-subagents"></a>

| Role | Domain | Primary Responsibilities | Default Scoped Skills |
|---|---|---|---|
| **`recon`** | Network Recon | Host discovery, port scanning, and service version enumeration. | `recon`, `smb-enum`, `snmp-enum` |
| **`web`** | Web Applications | Full-spectrum web assessment, fingerprinting, directory & parameter fuzzing. | `web`, `vuln-scanning`, `wordpress-assessment`, `api-pentest`, `parameter-fuzzing`, `js-recon` |
| **`api-hunter`** | API Pentesting | REST, GraphQL, and microservice testing; BOLA/IDOR and schema analysis. | `api-pentest`, `parameter-discovery`, `graphql-pentest`, `web` |
| **`js-miner`** | Client-Side Code | JavaScript bundle AST parsing, endpoint extraction, hardcoded secret mining. | `secret-mining`, `js-recon`, `web` |
| **`auth-auditor`** | Authentication | JWT analysis, token tampering, OAuth2 redirection, session validation. | `auth-logic`, `api-pentest`, `web` |
| **`cloud-recon`** | Cloud & Perimeter | Multi-cloud storage enumeration, subdomain takeover, perimeter auditing. | `cloud-recon`, `recon`, `osint-enum` |
| **`osint`** | Passive Recon | Domain intelligence, certificate search, passive subdomain gathering. | `osint-enum` |
| **`exploiter`** | Exploit Verification | Focused verification of identified vulnerabilities (SQLi, credentials). | `sql-injection`, `credential-attack` |
| **`source-analysis`** | Static Analysis | Source code pattern matching, hardcoded credential and logic audits. | `source-analysis` |
| **`reverser`** | Binary Analysis | Binary mitigation auditing, ELF/PE header analysis, disassembly triage. | `reverse-engineering` |

---

## Concurrent Delegation Fan-Out <a id="concurrent-handoff"></a>

A task can delegate work to multiple sub-agents in parallel:
```json
[HANDOFF] {
  "handoffs": [
    {"role": "api-hunter", "subgoal": "Extract Swagger schema and audit user endpoints for IDOR"},
    {"role": "js-miner", "subgoal": "Extract hardcoded API keys and hidden routes from app.bundle.js"}
  ]
}
```

### Delegation Lifecycle:
1. `DelegationCoordinator` enforces concurrency bounds (default max 4 per fan-out, 3 globally concurrent).
2. Each sub-agent runs asynchronously in its own goroutine.
3. Structured findings and discovered asset nodes are saved directly into the shared `knowledge.Store`.
4. An ANSI-stripped, threat-scanned concise summary is returned to the parent agent's observation history.

---

## Architectural Guard: Depth Capped at 1 <a id="depth-cap"></a>

Sub-agents are initialized with an **empty** sub-agent registry. A sub-agent cannot issue a `HANDOFF` to spawn another sub-agent. This structural constraint guarantees zero risk of unbounded recursive task creation or runaway API costs.

---

## Automated Skill Curator & Sanitization <a id="auto-skill"></a>

When a task trajectory succeeds, the `skillcurator` drafts a candidate skill:
1. **Sanitization (`sanitize.go`)**: Strips client-specific targets, IP addresses, credentials, and API keys into generic placeholders (`<TARGET>`, `<TOKEN>`).
2. **Threat Scan**: Scans the drafted YAML for prompt injection patterns.
3. **Quarantine (`pending_skills`)**: Placed into a pending queue requiring explicit operator review (`POST /api/v1/skills/pending/{id}/approve`) before activation.
