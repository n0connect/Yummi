# Controlled Web Research Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an operator-approved, read-only, source-attributed public-web research capability without weakening Yummi's fail-closed execution boundary.

**Architecture:** A new orchestrator `webresearch` package owns validation, provider contracts, evidence normalization, and audit-safe models. A dedicated research container performs only approved HTTPS fetches through the gateway; an adapter communicates with the configured search provider. Results are sanitized evidence, not browser output or trusted instructions.

**Tech Stack:** Go, SQLite, existing Go threatscan package, Rust gateway, Docker Compose, React/TypeScript UI.

**Spec:** `docs/superpowers/specs/2026-10-13-web-research-design.md`

## Global Constraints

- Research is disabled and deny-all until an operator supplies provider configuration and enables the research policy.
- No host-side agent-controlled network path; the dedicated container must traverse the gateway.
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

### Task 1: Research-domain models and untrusted-evidence normalizer

**Files:**
- Create: `orchestrator/internal/webresearch/types.go`
- Create: `orchestrator/internal/webresearch/normalize.go`
- Create: `orchestrator/internal/webresearch/normalize_test.go`

**Interfaces:**
- Produces: `Request`, `Candidate`, `Evidence`, `SourceFailure`, and `Normalize(candidate Candidate, body []byte, now time.Time) (Evidence, error)`.
- Consumes: `threatscan.StripANSI` and `threatscan.FirstThreatMessage`.

- [ ] **Step 1: Write failing normalization tests** for a valid HTML page, an HTML page containing an instruction-injection phrase, an oversized body, and a non-HTML content type. Assert valid evidence has URL/title/digest/excerpt/trust; unsafe input returns a typed omission error.
- [ ] **Step 2: Run** `cd orchestrator && go test ./internal/webresearch -run TestNormalize -count=1` **and confirm it fails because the package does not exist.**
- [ ] **Step 3: Implement minimal models and normalizer.** Use `net/url` for source URL parsing, SHA-256 over the bounded source body, stdlib HTML tokenization/text extraction, a 32 KiB raw-body ceiling, a 4 KiB excerpt ceiling, `StripANSI`, and `ScopeStrict` threat scanning. Return no raw body.
- [ ] **Step 4: Run the focused test** and confirm it passes.
- [ ] **Step 5: Commit** `feat(webresearch): normalize cited untrusted evidence`.

### Task 2: URL policy and DNS-pinned HTTPS fetcher

**Files:**
- Create: `orchestrator/internal/webresearch/policy.go`
- Create: `orchestrator/internal/webresearch/fetcher.go`
- Create: `orchestrator/internal/webresearch/fetcher_test.go`

**Interfaces:**
- Consumes: `Candidate`, `Evidence`, and `Normalize` from Task 1.
- Produces: `HostnamePolicy.Allows(host string) bool` and `Fetcher.Fetch(ctx context.Context, rawURL string) (Evidence, error)`.

- [ ] **Step 1: Write failing tests** with an injected resolver and HTTPS transport proving allowlisted HTTPS succeeds; HTTP, literal loopback/private/ULA addresses, an off-policy redirect, and a redirect to a private resolved address fail before fetching its body.
- [ ] **Step 2: Run** `cd orchestrator && go test ./internal/webresearch -run 'TestFetcher|TestURLPolicy' -count=1` **and observe missing policy/fetcher symbols.**
- [ ] **Step 3: Implement the minimum fetcher.** Canonicalize hosts, resolve before every hop, reject unsafe addresses, set `CheckRedirect` to rerun the policy, bind the transport dialer to the validated IP set, permit only HTTPS and `text/html`/`text/plain`, cap redirects at three and response bodies at 32 KiB.
- [ ] **Step 4: Run focused tests**, then `cd orchestrator && go test ./internal/webresearch -count=1`.
- [ ] **Step 5: Commit** `feat(webresearch): enforce pinned HTTPS source policy`.

### Task 3: Provider contract, disabled-by-default configuration, and service

**Files:**
- Create: `orchestrator/internal/webresearch/provider.go`
- Create: `orchestrator/internal/webresearch/service.go`
- Create: `orchestrator/internal/webresearch/service_test.go`
- Modify: `orchestrator/internal/config/config.go`
- Modify: `orchestrator/cmd/orchestrator/main.go`

**Interfaces:**
- Produces: `Provider.Search(ctx context.Context, query string, limit int) ([]Candidate, error)`, `Service.Research(ctx context.Context, Request) Report`, and `Capability{Available bool, Reason string}`.
- Consumes: Task 2 `Fetcher` and Task 1 evidence models.

- [ ] **Step 1: Write failing service/config tests** for no provider configuration, query/result-limit validation, a fake provider returning one valid and one blocked candidate, and errors that never include the configured key.
- [ ] **Step 2: Run** `cd orchestrator && go test ./internal/webresearch ./internal/config -run 'TestService|TestWebResearch' -count=1` **and confirm the new service/config behavior is absent.**
- [ ] **Step 3: Implement a provider-neutral service and disabled default.** Define configuration as provider endpoint/key plus approved hostnames; instantiate no provider when incomplete; validate query length and 1–5 limit; process candidates independently and return per-source failures. Do not add a scrape fallback.
- [ ] **Step 4: Run focused tests** and `cd orchestrator && go test ./internal/webresearch ./internal/config -count=1`.
- [ ] **Step 5: Commit** `feat(webresearch): add disabled-by-default research service`.

### Task 4: Gateway-enforced research container and policy separation

**Files:**
- Create: `research/Dockerfile`
- Create: `research/entrypoint.sh`
- Modify: `docker-compose.yml`
- Modify: `gateway/config/default.yaml`
- Modify: `gateway/src/config.rs`
- Modify: `gateway/src/policy/hostname.rs`
- Test: `gateway/src/policy/hostname.rs`
- Test: `gateway/src/config.rs`

**Interfaces:**
- Produces: a `yummi-research` container with no Docker socket and no published ports; named research hostname policy separate from assessment scope.
- Consumes: gateway’s existing fail-closed egress enforcement.

- [ ] **Step 1: Write failing Rust tests** proving a research hostname list defaults to deny-all and never changes the existing assessment hostname policy or egress mode.
- [ ] **Step 2: Run** `cd gateway && cargo test research -- --nocapture` **and confirm the policy field/behavior does not exist.**
- [ ] **Step 3: Add the smallest policy/config surface and container.** The container contains only a statically built fetch helper or minimal Go binary, runs non-root, has a read-only root filesystem plus a tmpfs workspace, joins only the gateway-routed network, and has no control-plane network aliases, mounts, tools, or browser dependencies.
- [ ] **Step 4: Run** `cd gateway && cargo test`, then `docker compose config` and a container-inspection test that asserts no exposed ports/socket mounts/capability additions.
- [ ] **Step 5: Commit** `feat(research): isolate research egress behind gateway policy`.

### Task 5: Executor/API integration, dedicated approval, and cited UI results

**Files:**
- Modify: `orchestrator/internal/agent/eino/pentest_tools.go`
- Modify: `orchestrator/internal/sandboxmcp/catalog.go`
- Modify: `orchestrator/internal/executor/executor.go`
- Modify: `orchestrator/internal/execution/store.go`
- Modify: `orchestrator/internal/api/routes.go`
- Create: `orchestrator/internal/api/handlers_research.go`
- Create: `orchestrator/internal/api/handlers_research_test.go`
- Modify: `ui/src/**` (exact existing task/result component selected after API contract is present)
- Test: `ui/src/**.test.tsx` (co-located with the selected result component)

**Interfaces:**
- Consumes: Task 3 `Service.Research` and Task 4 policy capability.
- Produces: `POST /api/v1/research` plus an explicit research-approval state and a UI evidence card with URL, source host, retrieval time, digest prefix, trust label, and safe excerpt.

- [ ] **Step 1: Write failing API tests** proving research is unavailable when disabled, a request waits for research approval, approval calls the service exactly once, and an evidence response has only safe citation fields/no raw HTML or provider key. Write a failing UI test for plain-text rendering and external-link attributes.
- [ ] **Step 2: Run focused Go and UI tests** and observe absent route/state/component failures.
- [ ] **Step 3: Implement the smallest integration.** Keep research separate from command execution and regular command approvals; persist audit-safe request/result metadata; render evidence as text, never `dangerouslySetInnerHTML`; preserve session isolation for all asynchronous calls.
- [ ] **Step 4: Run focused API/UI tests**, `cd orchestrator && go test ./...`, `cd ui && npm test`, `npm run build`, and `git diff --check`.
- [ ] **Step 5: Commit** `feat(webresearch): expose approved cited research results`.

### Task 6: Documentation and bounded live verification

**Files:**
- Create: `docs/agent/web-research.md`
- Modify: `AGENTS.md`
- Modify: `README.md`

**Interfaces:**
- Produces: operator setup, provider-key configuration, approval/audit model, source policy management, limitations, and a reproducible live smoke-test procedure.

- [ ] **Step 1: Write documentation assertions/checklist** naming disabled behavior, required provider configuration, gateway policy, evidence trust labels, and the no-browser/no-memory guarantees.
- [ ] **Step 2: Add the operator guide** with exact environment variable names selected in Task 3, compose profile/start requirements from Task 4, and the evidence fields returned by Task 5. State that live verification needs an operator-provided key and selected provider.
- [ ] **Step 3: Run** `rg -n 'TODO|TBD|<.*>' docs/agent/web-research.md` **and confirm it returns no placeholders; run all project validation gates feasible in the environment.**
- [ ] **Step 4: Commit** `docs: document controlled web research`.

## Plan self-review

- Spec coverage: Tasks 1–3 cover evidence, sanitization, provider configuration, and disabled mode; Task 4 covers isolated, gateway-enforced egress; Task 5 covers approval, audit, and UI; Task 6 covers deployment and live smoke evidence.
- Placeholder scan: no deferred behavior or unspecified implementation step is used; the UI file is deliberately selected only after its existing component contract is inspected in Task 5, so no stale path is asserted.
- Type consistency: `Request`, `Candidate`, `Evidence`, `Report`, `Provider`, `Fetcher`, `HostnamePolicy`, and `Capability` are introduced before consumers.
- Review focus: every listed failure mode has a named test in Tasks 1–4.
