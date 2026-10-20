# Security Policy

Yummi is a pentest tool — its whole job is to run security-sensitive commands against a target you control. This document has two purposes: how to report a vulnerability *in Yummi itself*, and — because "is this actually secure?" is a fair question to ask before you trust it — exactly what Yummi's security claims do and don't cover.

## Reporting a vulnerability

**Preferred: GitHub Private Vulnerability Reporting.** Open a draft security advisory at [github.com/n0connect/yummi/security/advisories/new](https://github.com/n0connect/yummi/security/advisories/new). This keeps the report private between you and the maintainer until a fix ships, and lets GitHub track it as a proper advisory (CVE assignment if warranted).

**Fallback:** if you can't use GitHub's flow, email **niceshotfree@gmail.com** with `[SECURITY]` in the subject. Include the affected component (orchestrator / gateway / sandbox / ui / sandbox-mcp), a reproduction, and impact.

**Do not open a public GitHub issue for a security vulnerability.** Everything else (feature requests, bugs that aren't security-sensitive, questions) is fine as a normal issue.

**Response time:** this is a solo-maintained project. Best-effort acknowledgment within 7 days, and a fix or mitigation plan communicated once triaged — not a contractual SLA, but a real commitment.

### Supported versions

Only the latest tagged release and the latest commit on `main` are supported. This is a young, actively-developed project without parallel maintenance branches — there is no older line still receiving backported fixes; upgrading to latest is the fix.

## What Yummi's security model actually claims — and what it doesn't

The distinction below matters more than it usually does for a project like this, because it's easy to read "fail-closed" and assume everything downstream of it inherits that guarantee. It doesn't. Network enforcement, operator approval, and risk classification provide different kinds of protection, with different limits:

### A real boundary: the gateway's network enforcement

`gateway` (Rust) installs `block_all` nftables rules in its own Linux network namespace before opening IPC. The checked-in Compose topology routes off-subnet sandbox traffic through that gateway. Operators may then choose restricted rules, per-destination approval, or `allow_all`. `block_all` denies forwarded sandbox traffic, including DNS and established connections; local/same-subnet traffic remains outside that FORWARD boundary.

A daemon crash preserves the **last committed rules**, not necessarily DROP-all. Existing static permissions or `allow_all` can remain while the namespace survives; kernel TTL rules still expire. Restart reinstalls `block_all`. Administrative `--cleanup` and namespace removal remove those rules. Kernel reconciliation attempts repairs and lockdown, and reports inconsistent enforcement if it cannot commit them. Source configuration and tests are not proof that a particular running deployment has been rebuilt with those controls.

This boundary does not decide what a command does inside the sandbox or against an allowed target. It also relies on sandbox routing/source assumptions: the container receives networking capabilities, selected tools have file capabilities, and its host workspace mount is writable. It is not a guarantee against container-root compromise or escape. Orchestrator-originated model/API traffic is outside the sandbox packet filter.

### A deterministic control: the human approval gate

Agent command approval is mode-driven. In `ask_permission`, every command waits for approval unless the operator has enabled the per-task low/medium-risk override. In `auto`, ordinary command approval is skipped, including for high/critical labels; detected destructive patterns force approval in both modes. Single-command chat proposals use a separate risk-based approval flow. Gateway destination approval is a separate control from command approval.

The gate uses an operator-resolved channel tied to the pending proposal. Structured MCP arguments must match the reviewed request. Content scanning can hold flagged tool output before model feedback; it detects known patterns and cannot guarantee protection against every prompt injection.

### A heuristic, not a boundary: risk classification and the conductor's judgment

The conductor's risk-level classification (`low`/`medium`/`high`/`critical`) labels proposals and informs risk-based approval paths — but the classification itself comes from an LLM's own structured output plus structural floors: recognized simple invocations are low, vetted local pipelines are medium, and unrecognized shell structures are critical. A separate destructive-pattern floor forces approval. This is a heuristic that reduces operator fatigue by not gating every single `ls`, not a security control you should rely on to catch something adversarial. In `auto`, risk labels alone do not block normal commands. Network policy, destructive-pattern gates, and content holds remain independent controls; a low-risk label does not establish that a command is harmless.

### Explicitly out of scope

- **The pentest itself.** Yummi finding a real vulnerability in a target you've authorized it against is the tool working, not a security bug in Yummi.
- **Misuse against unauthorized targets.** Yummi has no way to verify you have authorization to test a given target — that's on the operator, always. Nothing in this codebase substitutes for a signed engagement scope.
- **The Kali sandbox's own tool binaries** (nmap, sqlmap, hydra, etc.) — vulnerabilities in third-party pentest tools themselves are out of scope here; report those upstream.
- **Dormant code and third-party issues:** `orchestrator/internal/mcp` is a host-spawning client currently unimported by production code. A demonstrable route making it reachable from untrusted input is relevant to report; a hypothetical future integration is not an active execution path.

If you're evaluating Yummi for use and want more detail before relying on any of this, [docs/security-model.md](docs/security-model.md) and [docs/architecture.md](docs/architecture.md) go deeper than this document does.

## Data placement

Default inference uses local Ollama. Explicit compatible-model endpoints (including cloud OpenRouter/OpenAI), optional `brain/` providers, and sandbox `web_search` can send prompts/context or queries to external services. Sandbox-only research means HTTP, DNS, credentials, and parsing execute there under gateway policy; it does not mean the provider receives no data. See [configuration](docs/installation.md) and the [security model](docs/security-model.md).
