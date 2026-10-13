# Controlled Web Research — Design

**Date:** 2026-10-13  
**Status:** Proposed  
**Decision:** Build a read-only research authority, not a general-purpose browser mode.

## Goal

Let an operator-authorized Yummi task search public security intelligence and
return concise, source-attributed evidence without granting the agent arbitrary
internet browsing, cookie access, JavaScript execution, or a new host-side
network execution path.

## Scope

The first release provides a `web_research` agent tool. It accepts a text
query, runs it through a configured search-provider adapter, fetches a bounded
set of selected result pages through a dedicated research container, and
returns an evidence bundle: source URL, title, retrieval time, content digest,
sanitized excerpt, and trust label.

It does not authenticate to websites, submit forms, execute page JavaScript,
download files, store cookies, crawl arbitrary links, or write web content to
long-term memory automatically. It does not replace browser automation for an
in-scope assessment target.

## Trust boundaries

```
agent intent
  -> orchestrator/webresearch (query limits, provider API, audit record)
  -> research container (HTTP fetch only; no shell/MCP/browser APIs)
  -> gateway research egress policy (provider/source hostname allowlist)
  -> public web

untrusted response -> normalizer -> threatscan -> evidence bundle -> model/UI
```

The research container is distinct from the Kali sandbox. It has no Docker
socket, no pentest binaries, no browser profile, no credentials, and no route
to the Docker-internal control-plane services. It reaches public hosts only
via the existing gateway. Its default egress is deny-all.

The orchestrator remains the only caller of the research API. The feature must
never use `net/http` directly from the agent package or create a host-side
egress path.

## Source and network policy

`gateway/config/default.yaml` gains a named `research` hostname allowlist.
The operator enables research separately from the assessment egress mode. The
initial list contains the chosen search provider and public, authoritative
security sources; it is configuration, not agent input.

Every provider result and every HTTP redirect is canonicalized and checked
against that hostname policy before connection. Scheme is restricted to HTTPS.
Loopback, link-local, private, multicast, unspecified, and IPv6 ULA addresses
are rejected after every DNS resolution. DNS answers are pinned for the request
and all connections use the verified answer set. A redirect outside policy is
a per-source failure, never a fallback to unrestricted egress.

The v1 provider is an adapter selected by configuration. The implementation
ships no provider key and starts disabled when the provider configuration or
credential is absent. A live provider choice and key are operator-supplied
deployment configuration, never task input or repository data.

## Data contract and handling

`ResearchRequest` contains only `query`, `max_results` (1–5), and an optional
operator-visible source filter. `ResearchEvidence` contains `source_url`,
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

`web_research` is read-only but causes public egress and therefore requires an
explicit research approval by default. Approval names the query, provider,
candidate source hosts, and result limit. The regular command-risk approval
does not implicitly authorize research egress.

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
- Integration tests use a local fake provider/fetcher and prove an allowed
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
