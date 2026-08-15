# Security Policy

Yummi is a pentest tool — its whole job is to run security-sensitive commands against a target you control. This document has two purposes: how to report a vulnerability *in Yummi itself*, and — because "is this actually secure?" is a fair question to ask before you trust it — exactly what Yummi's security claims do and don't cover.

## Reporting a vulnerability

**Preferred: GitHub Private Vulnerability Reporting.** Open a draft security advisory at [github.com/n0connect/yummi/security/advisories/new](https://github.com/n0connect/yummi/security/advisories/new). This keeps the report private between you and the maintainer until a fix ships, and lets GitHub track it as a proper advisory (CVE assignment if warranted).

**Fallback:** if you can't use GitHub's flow, email **niceshotfree@gmail.com** with `[SECURITY]` in the subject. Include the affected component (orchestrator / gateway / sandbox / ui / sandbox-mcp), a reproduction, and impact.

**Do not open a public GitHub issue for a security vulnerability.** Everything else (feature requests, bugs that aren't security-sensitive, questions) is fine as a normal issue.

**Response time:** this is a solo-maintained project. Best-effort acknowledgment within 7 days, and a fix or mitigation plan communicated once triaged — not a contractual SLA, but a real commitment.

### Supported versions

Yummi hasn't cut a tagged release yet — until it does, only the latest commit on `main` is supported. Once versioned releases start, this section will list which lines still receive security fixes.

## What Yummi's security model actually claims — and what it doesn't

The distinction below matters more than it usually does for a project like this, because it's easy to read "fail-closed" and assume everything downstream of it inherits that guarantee. It doesn't. Two different things are happening in this codebase, and only one of them is a real boundary:

### A real boundary: the gateway's network enforcement

`gateway` (Rust) installs deny-all nftables rules on the Docker bridge **before its own IPC socket even opens** — the sandbox has zero network connectivity until an explicit, scoped allow rule lands. This is enforced by the Linux kernel's netfilter, independent of anything the orchestrator, the AI conductor, or a compromised/hallucinating model does or says. If gateway crashes, the deny-all rules persist (that's what "fail-closed" means here — failure removes capability, not access control). This boundary has been verified with real interface-counter tests (`docs/installation.md`'s Docker Desktop VM networking writeup is the artifact of that testing), not just asserted.

**What this does NOT cover:** gateway enforces network egress. It has no opinion about what a command does *inside* the sandbox against a target you've already scoped it to reach — that's the actual pentesting, and it's supposed to happen.

### A real boundary: the human approval gate

Any command classified medium risk or higher blocks the agent loop until an operator explicitly approves it via the UI or API. This is not a suggestion the model can talk itself out of — it's a blocking channel (`agent.Gate`) the loop cannot proceed past without an external signal. Combined with the content-layer threat scanner (`internal/security/threatscan`), which flags prompt-injection/exfiltration patterns in tool *output* before it re-enters the model's context, this is meant to survive a target actively trying to manipulate the agent, not just an honestly-behaving one.

### A heuristic, not a boundary: risk classification and the conductor's judgment

The conductor's risk-level classification (`low`/`medium`/`high`/`critical`) decides *which* commands need approval — but the classification itself comes from an LLM's own structured output plus a conservative structural floor (shell-chaining operators force `critical`). This is a heuristic that reduces operator fatigue by not gating every single `ls`, not a security control you should rely on to catch something adversarial. If you run Yummi in fully autonomous (`auto`) approval mode, you have turned this heuristic into your only remaining gate on the network path *inside* your already-scoped engagement — know that trade-off before choosing it.

### Explicitly out of scope

- **The pentest itself.** Yummi finding a real vulnerability in a target you've authorized it against is the tool working, not a security bug in Yummi.
- **Misuse against unauthorized targets.** Yummi has no way to verify you have authorization to test a given target — that's on the operator, always. Nothing in this codebase substitutes for a signed engagement scope.
- **The Kali sandbox's own tool binaries** (nmap, sqlmap, hydra, etc.) — vulnerabilities in third-party pentest tools themselves are out of scope here; report those upstream.
- **`internal/mcp`** — a host-spawning MCP client that exists in the codebase but is not imported by any production code path (dormant, unreachable). If this ever changes, it gets its own security review before being covered by anything above.

If you're evaluating Yummi for use and want more detail before relying on any of this, [docs/security-model.md](docs/security-model.md) and [docs/architecture.md](docs/architecture.md) go deeper than this document does.
