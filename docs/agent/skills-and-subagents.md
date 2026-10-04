[Back to Project README](../../README.md) · [Docs index](../README.md) · [Agent overview](overview.md)

# Skills & Sub-Agents

Two complementary mechanisms extend the agent's problem-solving capabilities without altering its deterministic execution core: **skills** (declarative, trigger-matched methodology injected into prompts) and **sub-agents** (bounded delegation to specialized personas).

---

## Table of Contents

- [Skills: Declarative Offensive Methodology](#skills)
- [Mechanical Skill Selection Algorithm](#skill-selection)
- [The 99 Built-in Red Team Skills](#builtin-skills)
- [Sub-Agents: Bounded Role Delegation](#subagents)
- [The 52 Specialized Sub-Agents](#builtin-subagents)
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

## The 99 Built-in Red Team Skills <a id="builtin-skills"></a>

The repository includes 99 declarative YAML offensive playbooks stored under `persona/v0.1/skills/`, categorized by operational domain:

### 1. Web Applications & Architecture
- **`web`** — Fingerprinting, headers, WAF triage, and baseline discovery (`whatweb`, `curl`, `gobuster`, `nikto`).
- **`js-recon`** & **`spa-modern-recon`** — JavaScript endpoint harvesting, AST route mapping, webpack bundle inspection (`katana`, `gau`, `curl`).
- **`secret-mining`** — Extraction of hardcoded API keys, JWT secrets, and tokens from frontend bundles (`jsluice`, `curl`).
- **`parameter-discovery`** & **`parameter-fuzzing`** — Heuristic query/body parameter brute-forcing (`arjun`, `ffuf`).
- **`modern-xss-csp`** — Context-aware XSS exploitation, CSP bypass techniques, and DOM sink analysis.
- **`cors-csrf`** — Origin reflection, null origin abuse, and cross-site request forgery validation.
- **`open-redirect`** — Parameter validation, bypass techniques for common redirect filters.
- **`information-disclosure`** — Sensitive file hunting (`.git`, `.env`, backup files, stack traces).
- **`web-cache-deception`** & **`header-injection-smuggling`** — Web cache deception and HTTP request smuggling (`http-request-smuggling`).
- **`wordpress-assessment`** — Plugin/theme vulnerability scanning and user enumeration (`wpscan`).
- **`django-framework-audit`** & **`nestjs-framework-audit`** & **`nextjs-react-security`** — Modern web framework security patterns and Server Actions auditing.

### 2. Injection, Exploitation & Logic Flaws
- **`sql-injection`** & **`nosql-injection`** — Automated and manual SQLi/NoSQLi verification (`sqlmap`, `curl`).
- **`command-injection`** — Blind and in-band OS command injection with filter evasion techniques.
- **`path-traversal-lfi`** — Directory traversal and Local File Inclusion auditing.
- **`ssti`** — Server-Side Template Injection across Jinja2, Twig, Freemarker, and ERB engines.
- **`xxe`** & **`xxe-advanced`** — XML External Entity injection (billion laughs, OOB exfiltration, SSRF via XML).
- **`ssrf`** & **`ssrf-advanced`** — Server-Side Request Forgery targeting cloud metadata services (`169.254.169.254`) and internal subnets.
- **`deserialization-attacks`** & **`insecure-deserialization`** — Java/Python/PHP object gadget chain analysis.
- **`prototype-pollution`** — Client-side and server-side JavaScript prototype tampering.
- **`file-upload`** & **`file-upload-advanced`** — Extension bypass, polyglot files, and double-extension execution.
- **`business-logic`** & **`race-conditions`** — Multi-threaded concurrency bugs, TOCTOU, and transaction tampering.
- **`idor-bfla`** & **`mass-assignment`** — Broken Object-Level Authorization and privilege escalation via parameter injection.

### 3. APIs, Microservices & Data Stores
- **`api-pentest`** & **`api-security-advanced`** — RESTful API testing, BOLA/BFLA auditing, and method tampering.
- **`graphql-pentest`** & **`graphql-advanced-attacks`** — Introspection queries, batching, alias overload, and depth attacks (`graphql_cop`).
- **`grpc-security`** — Protocol Buffers inspection, gRPC reflection, and service endpoint auditing.
- **`websocket-security`** — CSWSH (Cross-Site WebSocket Hijacking), message tampering, and authorization checks.
- **`database-security`** — Direct database auditing (MySQL, PostgreSQL, MongoDB, Redis, MSSQL).
- **`baas-cloud-databases`** & **`supabase-security`** & **`firebase-security`** — Row-Level Security (RLS) bypass, misconfigured Firestore rules, and exposed service keys.

### 4. Identity, Authentication & Access Control
- **`auth-logic`** & **`jwt-auth-attacks`** — Signature stripping (`none`), key confusion (HMAC/RSA), and claim tampering (`jwt_tool`).
- **`oauth-oidc`** & **`saml-sso-attacks`** — Redirect URI manipulation, state parameter omissions, XML signature wrapping, and IdP spoofing.
- **`auth0-tenant-abuse`** — Misconfigured custom grant types, open user registration, and rules/actions tampering.
- **`credential-attack`** & **`password-spraying-smart`** — Time-distributed spraying, lock-out awareness, and service brute-forcing (`hydra`, `medusa`).

### 5. Active Directory & Windows Enterprise
- **`active-directory`** & **`ad-kerberos-advanced`** — Kerberoasting, AS-REP Roasting, Pass-the-Hash, Pass-the-Ticket (`impacket`, `netexec`).
- **`adcs-certificate-abuse`** — AD Certificate Services misconfigurations (ESC1 through ESC8) via `certipy`.
- **`ldap-injection`** — LDAP filter manipulation, blind attribute exfiltration, and schema queries.
- **`smb-enum`** — SMB share enumeration, null session inspection, and signing checks (`enum4linux`, `smbclient`).
- **`windows-privesc`** — Token impersonation, unquoted service paths, always-install-elevated, and potato exploits.

### 6. Cloud Infrastructure, Containers & CI/CD
- **`cloud-recon`** & **`cloud-security`** — Public storage bucket auditing (S3, GCS, Azure Blobs) and perimeter mapping (`cloud_enum`).
- **`cloud-iam-privesc`** & **`cloud-privilege-escalation`** — IAM role assumption, policy misconfigurations, and metadata token abuse.
- **`container-security`** & **`k8s-cluster-security`** — Docker breakout, privileged containers, Kubernetes API discovery, service account token extraction.
- **`ci-cd-security`** & **`supply-chain-security`** — GitHub Actions runners, exposed GitLab tokens, and build pipeline tampering.
- **`sast-dependency-audit`** — Third-party library vulnerabilities and known CVE dependency matching.

### 7. Network Reconnaissance, Pivoting & Perimeter
- **`recon`** & **`osint-enum`** — Port scanning, service versioning, DNS enumeration (`nmap`, `subfinder`, `amass`, `dnsx`).
- **`subdomain-takeover`** & **`subdomain-takeover-advanced`** — Dangling DNS records, CNAME verification, and cloud hosting checks (`subzy`).
- **`network-pivoting`** — Double pivoting, SSH dynamic port forwarding, Chisel, and Ligolo-ng tunnels.
- **`network-sniffing-mitm`** — ARP spoofing, traffic interception, and LLMNR/NBT-NS poisoning (`responder`).
- **`edge-vpn-gateways`** — SSL VPN and gateway vulnerability assessments (Pulse Secure, Fortinet, SonicWall).
- **`snmp-enum`** — SNMP community string brute-forcing and MIB walking (`onesixtyone`, `snmpwalk`).
- **`email-dns-security`** — SPF, DKIM, DMARC records, and SMTP open relay testing.

### 8. Systems, Binary & Hardware/RF
- **`privesc-linux`** — SUID binaries, sudo privileges, kernel exploits, cron jobs, and capabilities auditing.
- **`reverse-engineering`** & **`memory-runtime-analysis`** — ELF/PE headers, mitigations (ASLR, DEP, PIE), GDB/Radare2 inspection (`checksec`, `radare2`).
- **`mobile-security-assessment`** — APK decompilation, exported activities, insecure storage, and hardcoded secrets.
- **`iot-embedded-security`** — Firmware extraction, UART/JTAG pinout analysis, and embedded web interface audits.
- **`wireless-security-audit`** & **`subghz-rf-security`** — WPA2/3 handshake capture, rogue AP detection, and Sub-GHz RF replay analysis.
- **`llm-security`** — Prompt injection, system prompt leakage, and guardrail bypass testing.

### 9. Adversary Emulation, Threat Hunting & Defense
- **`apt-adversary-emulation`** — MITRE ATT&CK mapping, staged persistence, and realistic TTP simulation.
- **`red-team-c2-operations`** — C2 channel setup, covert communication, and egress routing.
- **`defense-evasion-stealth`** — AMSI bypass, log stomping, process injection, and living-off-the-land techniques.
- **`dfir-triage-investigation`** — Volatile artifact collection, memory dumping, and event log timeline analysis.
- **`detection-rule-generation`** — Sigma and YARA rule authoring based on executed attack trajectories.
- **`monitoring-telemetry-security`** & **`grafana-prometheus-audit`** — Metrics endpoint security, unauthenticated dashboard access, and alert tampering.
- **`poc-verification`** & **`attack-chain-discovery`** — Multi-step vulnerability verification and exploit chain linking.

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

## The 52 Specialized Sub-Agents <a id="builtin-subagents"></a>

The repository provides 52 specialized sub-agents defined in `persona/v0.1/subagents/`:

### Active Directory & Enterprise (5 Roles)
| Role | Primary Responsibility | Scoped Skills |
|---|---|---|
| **`ad-operator`** | Domain reconnaissance, BloodHound collection, user/computer mapping. | `active-directory`, `smb-enum`, `ldap-injection` |
| **`adcs-specialist`** | Certificate templates inspection, ESC1-ESC8 abuse, PKINIT authentication. | `adcs-certificate-abuse`, `active-directory` |
| **`kerberos-specialist`** | Kerberoasting, AS-REP roasting, silver/golden ticket generation, unconstrained delegation. | `ad-kerberos-advanced`, `active-directory` |
| **`smart-spray-operator`** | Smart password spraying with lockout thresholds and timing jitter. | `password-spraying-smart`, `credential-attack` |
| **`windows-operator`** | Windows local privilege escalation, DPAPI secrets, token manipulation. | `windows-privesc`, `active-directory` |

### Web Applications & Modern APIs (10 Roles)
| Role | Primary Responsibility | Scoped Skills |
|---|---|---|
| **`web`** | Core web application assessment, directory enumeration, fingerprinting. | `web`, `vuln-scanning`, `parameter-fuzzing` |
| **`api-hunter`** | REST and microservice testing, BOLA/IDOR, schema analysis. | `api-pentest`, `parameter-discovery`, `idor-bfla` |
| **`modern-api-specialist`** | GraphQL, gRPC, and WebSocket security auditing. | `graphql-pentest`, `grpc-security`, `websocket-security` |
| **`js-miner`** | JavaScript AST parsing, endpoint extraction, client-side secret mining. | `secret-mining`, `js-recon` |
| **`auth-auditor`** | Authentication flow verification, password resets, session handling. | `auth-logic`, `business-logic` |
| **`token-specialist`** | JWT header injection, key confusion, algorithm manipulation. | `jwt-auth-attacks`, `auth-logic` |
| **`sso-saml-specialist`** | SAML assertions, OAuth2 redirect manipulation, OIDC claims. | `saml-sso-attacks`, `oauth-oidc` |
| **`saas-identity-specialist`** | Multi-tenant SaaS access control and tenant isolation audits. | `auth0-tenant-abuse`, `idor-bfla` |
| **`spa-hunter`** | Single Page Application route reconstruction and client-side logic audits. | `spa-modern-recon`, `modern-xss-csp` |
| **`cache-specialist`** | Cache poisoning, web cache deception, and hop-by-hop headers. | `web-cache-deception`, `header-injection-smuggling` |

### Cloud, Container & Infrastructure (9 Roles)
| Role | Primary Responsibility | Scoped Skills |
|---|---|---|
| **`cloud-operator`** | Multi-cloud resource enumeration (AWS, Azure, GCP). | `cloud-security`, `cloud-recon` |
| **`cloud-recon`** | Public storage discovery, dangling assets, perimeter mapping. | `cloud-recon`, `subdomain-takeover` |
| **`cloud-iam-pivoter`** | Cloud credential harvesting, IAM policy evaluation, role chaining. | `cloud-iam-privesc`, `cloud-security` |
| **`cloud-privesc-specialist`**| Metadata abuse, privilege escalation paths within cloud providers. | `cloud-privilege-escalation`, `ssrf-advanced` |
| **`baas-cloud-auditor`** | Supabase, Firebase, and Appwrite backend database and RLS rules. | `baas-cloud-databases`, `supabase-security` |
| **`container-operator`** | Container breakout, capability auditing, Docker daemon socket exposure. | `container-security`, `privesc-linux` |
| **`k8s-operator`** | Kubernetes RBAC, service account token theft, pod-to-pod pivoting. | `k8s-cluster-security`, `container-security` |
| **`ci-cd-auditor`** | Pipeline configuration auditing, runner escape, secret exfiltration. | `ci-cd-security`, `supply-chain-security` |
| **`supply-chain-auditor`** | Dependency vulnerability mapping and software component security. | `supply-chain-security`, `sast-dependency-audit` |

### Network Reconnaissance, Perimeter & Pivoting (7 Roles)
| Role | Primary Responsibility | Scoped Skills |
|---|---|---|
| **`recon`** | Network scanning, port discovery, service banner extraction. | `recon`, `vuln-scanning` |
| **`osint`** | Passive reconnaissance, certificate transparency, domain records. | `osint-enum`, `email-dns-security` |
| **`perimeter-operator`** | Edge VPN gateways, external firewalls, exposed infrastructure. | `edge-vpn-gateways`, `recon` |
| **`pivot-operator`** | Internal network pivoting, multi-hop port forwarding, tunnel routing. | `network-pivoting`, `smb-enum` |
| **`mitm-operator`** | Local network traffic interception, ARP/DNS spoofing, credential sniffing. | `network-sniffing-mitm` |
| **`protocol-auditor`** | Proprietary and legacy network protocol auditing (SNMP, Telnet, FTP). | `snmp-enum`, `recon` |
| **`database-specialist`** | SQL/NoSQL database service discovery, default credentials, RCE. | `database-security`, `sql-injection` |

### Exploitation, Gadgets & Reverse Engineering (7 Roles)
| Role | Primary Responsibility | Scoped Skills |
|---|---|---|
| **`exploiter`** | Exploit execution against verified injection and authentication flaws. | `sql-injection`, `command-injection` |
| **`poc-validator`** | Deterministic Proof-of-Concept generation and validation without disruption. | `poc-verification`, `business-logic` |
| **`exploit-chain-specialist`** | Assembling multi-step exploit chains (e.g. LFI -> SSRF -> RCE). | `attack-chain-discovery`, `ssrf-advanced` |
| **`deser-gadget-hunter`** | Serialization payloads, gadget chain hunting across runtime environments. | `deserialization-attacks`, `insecure-deserialization` |
| **`lfi-traversal-specialist`** | Path traversal filter bypass, log poisoning, procfs inspection. | `path-traversal-lfi` |
| **`reverser`** | Binary analysis, mitigation auditing, static and dynamic disassembly. | `reverse-engineering` |
| **`memory-analyst`** | Runtime heap/stack analysis, format strings, memory corruption triage. | `memory-runtime-analysis`, `reverse-engineering` |

### Adversary Emulation, Threat Response & Defense (7 Roles)
| Role | Primary Responsibility | Scoped Skills |
|---|---|---|
| **`apt-emulation-operator`** | Multi-phase APT attack campaign emulation following MITRE ATT&CK. | `apt-adversary-emulation`, `red-team-c2-operations` |
| **`evasion-specialist`** | Stealth operational techniques, payload obfuscation, log evasion. | `defense-evasion-stealth` |
| **`detection-engineer`** | Analyzing attack telemetry and generating Sigma/YARA detection rules. | `detection-rule-generation` |
| **`dfir-investigator`** | Digital forensics triage, artifact timeline reconstruction. | `dfir-triage-investigation` |
| **`source-analysis`** | Static source code review for dangerous functions and credential leaks. | `source-analysis` |
| **`logic-hunter`** | High-level business logic flaws, state machine bypasses, rate limiting. | `business-logic`, `race-conditions` |
| **`info-leak-hunter`** | Git repositories, debug endpoints, sensitive backup inspection. | `information-disclosure`, `largest-files` |

### Hardware, RF, Mobile & AI Security (7 Roles)
| Role | Primary Responsibility | Scoped Skills |
|---|---|---|
| **`ai-redteam-operator`** | LLM prompt injection, jailbreaking, agentic hijacking. | `llm-security` |
| **`mobile-app-hunter`** | Mobile application reverse engineering, intent sniffing, API communication. | `mobile-security-assessment` |
| **`iot-firmware-analyst`** | Embedded device firmware unpacking, binary auditing, hardcoded credentials. | `iot-embedded-security` |
| **`wireless-rf-operator`** | Wi-Fi network auditing, WPA handshake extraction, rogue APs. | `wireless-security-audit` |
| **`rf-sdr-specialist`** | Software-Defined Radio, Sub-GHz signal replay and analysis. | `subghz-rf-security` |
| **`email-security-specialist`**| Mail infrastructure auditing, spoofing, open relays, SPF/DKIM/DMARC. | `email-dns-security` |
| **`privesc-operator`** | Linux kernel, SUID, and service privilege escalation. | `privesc-linux`, `system-inspection` |

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
