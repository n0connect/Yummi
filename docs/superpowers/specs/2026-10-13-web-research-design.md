# Controlled Web Research — Design

**Date:** 2026-10-13  
**Status:** Suspended — not an executable implementation brief.

**2026-10-13 correction:** The prior design and plan are superseded by the
operator's sandbox-only requirement. No research HTTP/DNS/provider operation
may run on the host or in the orchestrator. The subsequently authorized
sandbox-only delivery is described in `docs/agent/web-research.md` and
`docs/superpowers/plans/2026-10-13-browser-research-quality.md`. Search/fetch are
now registered there. Validation executes in Docker using real components,
with no new mocks or provider substitutes. This document remains historical
rationale, not the active implementation brief.
**Decision:** Build a read-only research authority inside the existing sandbox,
not a general-purpose browser mode or host-side service.

## Goal

Let an operator-authorized Yummi task search public security intelligence and
return concise, source-attributed evidence without granting the agent arbitrary
internet browsing, cookie access, JavaScript execution, or any new
orchestrator/host-side network execution path.

## Scope

The first release provides two sandbox-MCP tools: `web_search` queries the
configured provider and returns citations; `web_fetch_source` fetches one
operator-scoped result URL and returns a bounded evidence bundle: source URL,
title, retrieval time, content digest, sanitized excerpt, and trust label.

It does not authenticate to websites, submit forms, execute page JavaScript,
download files, store cookies, crawl arbitrary links, or write web content to
long-term memory automatically. It does not replace browser automation for an
in-scope assessment target.

## Trust boundaries

```
agent intent
  -> orchestrator (MCP dispatch and audit record only; no HTTP/DNS)
  -> sandbox-mcp web-research tools (query, provider API, fetch, normalization)
  -> gateway research egress policy (provider/source hostname allowlist)
  -> public web

untrusted response -> normalizer -> threatscan -> evidence bundle -> model/UI
```

The sandbox-MCP process is the sole research-network client. It runs as the
existing unprivileged sandbox user and reaches public hosts only through the
existing gateway. Its default egress is deny-all. The orchestrator remains a
MCP caller only: it must never use `net/http`, a provider SDK, DNS resolution,
or a browser for research.

## Source and network policy

`gateway/config/default.yaml` gains a named `research` hostname allowlist.
The operator enables research separately from the assessment egress mode. The
initial list contains the chosen search provider and public, authoritative
security sources; it is configuration, not agent input.

The provider endpoint is fixed in sandbox configuration and never comes from
agent arguments. `web_fetch_source` is scoped by its explicit source URL via
the existing gateway path. Every provider result and every HTTP redirect is
canonicalized and checked against policy before connection. Scheme is HTTPS.
Loopback, link-local, private, multicast, unspecified, and IPv6 ULA addresses
are rejected after every DNS resolution. DNS answers are pinned for the request
and all connections use the verified answer set. A redirect outside policy is
a per-source failure, never a fallback to unrestricted egress.

The v1 provider is an adapter selected by configuration. The implementation
ships no provider key and starts disabled when the provider configuration or
credential is absent. A live provider choice and key are operator-supplied
deployment configuration, never task input or repository data.

## Data contract and handling

`web_search` accepts only `query` and `max_results` (1–5). `web_fetch_source`
accepts only the selected `source_url`. `ResearchEvidence` contains `source_url`,
`title`, `retrieved_at`, `sha256`, `excerpt`, `trust` (`primary`, `secondary`,
or `unclassified`), and a diagnostic status.

Raw bodies are size-capped, retained only in the short-lived research container
workspace, and excluded from task memory. The orchestrator receives normalized
plain text only. Before an excerpt becomes model-visible it is ANSI-stripped,
passed through `threatscan.ScopeStrict`, capped, and wrapped as untrusted
external content. A threat-scanned result is omitted with a safe diagnostic;
it is never summarized or persisted.

The task execution/audit record stores query, provider name, policy decision,
source host, timestamps, status, and content digest. It does not store API
keys, cookies, raw HTML, or full result bodies. The UI renders source links and
trust labels, never executable HTML.

## Agent and approval behavior

The tools are read-only but cause public egress and therefore retain the
existing MCP execution approval and gateway egress approval. `web_search`
names the fixed provider and query; `web_fetch_source` names its selected
source URL. The regular command-risk approval does not open external traffic.

The tool's result is evidence, not instruction. Prompts must tell the model to
treat excerpts as untrusted factual material, never as tool directives or
authorization. Findings derived from research must retain their source URLs and
remain `candidate` until independently verified.

## Failure behavior

All policy, DNS, provider, timeout, content-type, size-limit, parser, and
threatscan failures fail closed per source. Partial results are valid and carry
their individual failures. An unavailable provider makes the capability
unavailable; no browser or shell fallback is allowed.

## Validation requirements

- Unit tests prove URL canonicalization, private-address rejection, redirect
  revalidation, result limits, content-type/size bounds, and API-key redaction.
- Integration tests use the real sandbox fetcher and configured provider to prove an allowed
  result is normalized and cited while an off-policy redirect never connects.
- Threatscan tests prove prompt-injection-shaped response content does not
  reach the model-facing evidence bundle or persistent knowledge.
- Gateway tests prove research policy cannot change assessment egress and
  starts fail-closed.
- A live, operator-configured smoke test is documented but is not treated as a
  substitute for the deterministic suite.

## Explicit non-goals

- General web browser automation or browser session reuse.
- Search-engine scraping as an undeclared production dependency.
- Bypassing CAPTCHA, paywalls, robots controls, access controls, or rate limits.
- Automatically treating search output as confirmed security findings.
