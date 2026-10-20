# Controlled Web Research Implementation Plan

> **Historical record —** This dated plan/report describes its recorded revision and environment. It is not current implementation or deployment verification. Use the [maintained documentation](../../../docs/README.md) for current behavior. Original findings and results are preserved below.

**SUSPENDED / SUPERSEDED — do not execute the steps below.**
The operator prohibited all host-side execution and all mocks. The earlier
steps incorrectly proposed host `go test`, injected resolvers, and a fake
provider. They are retained only as history after the corrections below.
The subsequent authorized implementation is documented in
`docs/superpowers/plans/2026-10-13-browser-research-quality.md` and
`docs/agent/web-research.md`. Search/fetch are now registered inside sandbox-MCP.
The historical steps below remain superseded and must not be executed.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an operator-approved, read-only, source-attributed public-web research capability without weakening Yummi's fail-closed execution boundary.

**Architecture:** Sandbox-MCP owns validation, provider communication, HTTPS fetching, evidence normalization, and audit-safe models. The orchestrator only dispatches those typed MCP calls through its existing execution record/gateway path; it never performs research HTTP or DNS. Results are sanitized evidence, not browser output or trusted instructions.

**Tech Stack:** Go, SQLite, existing Go threatscan package, Rust gateway, Docker Compose, React/TypeScript UI.

**Spec:** `docs/superpowers/specs/2026-10-13-web-research-design.md`

## Global Constraints

- Research is disabled and deny-all until an operator supplies provider configuration and enables the research policy.
- No new host- or orchestrator-side network path; every research HTTP/DNS operation runs in the existing unprivileged sandbox and traverses the gateway.
- HTTPS only; redirect and DNS resolution must be revalidated; private, loopback, link-local, multicast, unspecified, and IPv6 ULA addresses are rejected.
- No JavaScript, cookies, custom headers, uploads, browser storage, file downloads, or raw HTML returned to the model/UI.
- Every model-visible excerpt is size-bounded, ANSI-stripped, strict-threatscanned, marked untrusted, and source-attributed.
- No automatic long-term-memory writes from research output.
- Provider keys are environment-only and must never enter events, logs, persistence, UI, or error messages.

## Review Focus

- A redirect from an allowed hostname to `127.0.0.1`, RFC1918, or IPv6 ULA must be rejected before any connection.
- A DNS rebinding answer after policy validation must not let the fetcher connect to a newly resolved private address.
- A search result containing prompt-injection text must be omitted from the evidence bundle, not merely labelled.
- A malformed provider response or one oversized/non-HTML response must leave other valid results usable without a browser fallback.
- A missing provider key or disabled research policy must expose an unavailable capability, not silently enable unrestricted egress.

---

### Task 1: Sandbox research models and untrusted-evidence normalizer

**Files:**
- Create: `sandbox-mcp/web_research.go`
- Create: `sandbox-mcp/web_research_test.go`

**Interfaces:**
- Produces: `researchCandidate`, `researchEvidence`, and `normalizeResearchEvidence(candidate researchCandidate, body []byte, now time.Time) (researchEvidence, error)`.
- Consumes: a local, conservative content-injection detector because sandbox-mcp deliberately has no orchestrator dependency.

- [ ] **Step 1: Write failing normalization tests** for a valid HTML page, an HTML page containing an instruction-injection phrase, an oversized body, and a non-HTML content type. Assert valid evidence has URL/title/digest/excerpt/trust; unsafe input returns a typed omission error.
- [ ] **Step 2: Run** `cd sandbox-mcp && go test -run TestNormalizeResearchEvidence -count=1` **and confirm it fails because the symbols do not exist.**
- [ ] **Step 3: Implement minimal models and normalizer.** Use `net/url`, SHA-256 over the bounded source body, stdlib HTML tokenization/text extraction, a 32 KiB raw-body ceiling, a 4 KiB excerpt ceiling, ANSI stripping, and a conservative injection detector. Return no raw body.
- [ ] **Step 4: Run the focused test** and confirm it passes.
- [ ] **Step 5: Commit** `feat(webresearch): normalize cited untrusted evidence`.

### Task 2: Sandbox-only URL policy and HTTPS fetcher

**Files:**
- Modify: `sandbox-mcp/web_research.go`
- Modify: `sandbox-mcp/web_research_test.go`

**Interfaces:**
- Consumes: `Candidate`, `Evidence`, and `Normalize` from Task 1.
- Produces: `researchFetch(ctx context.Context, rawURL string) (researchEvidence, error)`.

- [ ] **Step 1: Superseded.** Future tests must exercise the real sandbox resolver and HTTPS transport inside Docker; no injected dependencies are permitted.
- [ ] **Step 2: Run** `cd sandbox-mcp && go test -run 'TestResearchFetch|TestResearchURL' -count=1` **and observe missing policy/fetcher symbols.**
- [ ] **Step 3: Implement the minimum fetcher.** Canonicalize hosts, resolve before every hop, reject unsafe addresses, set `CheckRedirect` to rerun the policy, bind the transport dialer to the validated IP set, permit only HTTPS and `text/html`/`text/plain`, cap redirects at three and response bodies at 32 KiB.
- [ ] **Step 4: Run focused tests**, then `cd sandbox-mcp && go test ./...`.
- [ ] **Step 5: Commit** `feat(webresearch): enforce pinned HTTPS source policy`.

### Task 3: Provider contract, disabled-by-default sandbox configuration, and MCP tools

**Files:**
- Modify: `sandbox-mcp/web_research.go`
- Modify: `sandbox-mcp/web_research_test.go`
- Modify: `sandbox-mcp/tools.go`
- Modify: `sandbox/Dockerfile`
- Modify: `docker-compose.yml`

**Interfaces:**
- Produces: `web_search` and `web_fetch_source` MCP tools. Their provider configuration is environment-only in the sandbox container.
- Consumes: Task 2 fetcher and Task 1 evidence models.

- [ ] **Step 1: Superseded.** Future tests must exercise real provider configuration inside Docker, including disabled mode, validation and credential redaction; no fake provider is permitted.
- [ ] **Step 2: Run** `cd sandbox-mcp && go test -run 'TestWebSearch|TestWebResearch' -count=1` **and confirm the new behavior is absent.**
- [ ] **Step 3: Implement the smallest provider adapter and disabled default.** Read a fixed provider endpoint/key only from sandbox environment, validate query length and 1–5 limit, and do not add a scrape or browser fallback.
- [ ] **Step 4: Run focused tests** and `cd sandbox-mcp && go test ./...`.
- [ ] **Step 5: Commit** `feat(webresearch): add disabled-by-default research service`.

### Task 4: Gateway policy and orchestration integration

**Files:**
- Modify: `gateway/config/default.yaml`
- Modify: `gateway/src/config.rs`
- Modify: `gateway/src/policy/hostname.rs`
- Test: `gateway/src/policy/hostname.rs`
- Test: `gateway/src/config.rs`

**Interfaces:**
- Produces: a named research hostname policy and typed MCP catalog entries. Existing sandbox networking remains the only research execution environment.
- Consumes: gateway’s existing fail-closed egress enforcement and executor MCP dispatch.

- [ ] **Step 1: Write failing Rust tests** proving a research hostname list defaults to deny-all and never changes the existing assessment hostname policy or egress mode.
- [ ] **Step 2: Run** `cd gateway && cargo test research -- --nocapture` **and confirm the policy field/behavior does not exist.**
- [ ] **Step 3: Add the smallest policy/config surface and catalog wiring.** The existing sandbox-MCP process remains non-root and gateway-routed; no Docker socket, ports, capabilities, browser dependency, or host process is added.
- [ ] **Step 4: Run** `cd gateway && cargo test`, `cd orchestrator && go test ./internal/sandboxmcp ./internal/executor`, and `docker compose config`.
- [ ] **Step 5: Commit** `feat(research): isolate research egress behind gateway policy`.

### Task 5: Eino integration, documentation, and bounded live verification

**Files:**
- Modify: `orchestrator/internal/agent/eino/pentest_tools.go`
- Modify: `orchestrator/internal/sandboxmcp/catalog.go`
- Modify: `orchestrator/internal/executor/executor.go`
- Modify: `orchestrator/internal/agent/eino/pentest_tools.go`
- Modify: `orchestrator/internal/sandboxmcp/catalog.go`
- Modify: `orchestrator/internal/sandboxmcp/catalog_test.go`
- Create: `docs/agent/web-research.md`
- Modify: `AGENTS.md`

**Interfaces:**
- Consumes: Task 3 `Service.Research` and Task 4 policy capability.
- Produces: typed agent tools with cited plain-text output through the existing MCP execution/audit path, plus operator setup documentation.

- [ ] **Step 1: Write failing catalog and Eino tests** proving `web_search` has no model-controlled provider endpoint, `web_fetch_source` carries its URL through the existing scope check, and neither exposes headers, cookies, or scripts.
- [ ] **Step 2: Run focused tests** and observe absent catalog/spec failures.
- [ ] **Step 3: Implement the smallest integration and guide.** Register tools through the existing MCP execution/audit path, expose only query/result-limit/source URL fields, and document gateway/operator configuration and sandbox-only guarantees.
- [ ] **Step 4: Run** `cd orchestrator && go test ./...`, `cd sandbox-mcp && go test ./...`, `cd gateway && cargo test`, `make test`, and `git diff --check`.
- [ ] **Step 5: Commit** `feat(webresearch): add sandbox-confined cited research`.

## Plan self-review

- Spec coverage: Tasks 1–3 cover evidence, sanitization, provider configuration, and disabled mode; Task 4 covers isolated, gateway-enforced egress; Task 5 covers approval, audit, and UI; Task 6 covers deployment and live smoke evidence.
- Placeholder scan: no deferred behavior or unspecified implementation step is used; the UI file is deliberately selected only after its existing component contract is inspected in Task 5, so no stale path is asserted.
- Type consistency: `Request`, `Candidate`, `Evidence`, `Report`, `Provider`, `Fetcher`, `HostnamePolicy`, and `Capability` are introduced before consumers.
- Review focus: every listed failure mode has a named test in Tasks 1–4.
