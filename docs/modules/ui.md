← [Back to README](../../README.md) · [Docs index](../README.md)

# Module: ui (React + Vite)

The web dashboard — the only piece of this repository built with Node/npm rather than Go or Rust. Served in production by nginx inside its own container (`ui/Dockerfile`: `node:22-alpine` build stage → `nginx:1.27-alpine` runtime stage), proxying `/api` and `/ws` to orchestrator (`ui/nginx.conf`). See [Usage Guide](../usage-guide.md) for what each tab actually does — this page is the source-tree map.

## Table of contents

- [Directory structure](#directory-structure)
- [Why `features/` and `pages/` are separate](#features-vs-pages)
- [State management: no global store, one hook per data source](#state-management)
- [The one "mock"-adjacent file](#legitimate-mocks)

---

## Directory structure <a id="directory-structure"></a>

```
ui/src/
├── app/            App.tsx — top-level routing between the six tabs (routes past the
│                   first are React.lazy-loaded behind a Suspense boundary)
├── entities/       types.ts — shared TypeScript types mirroring orchestrator's JSON shapes
├── pages/          One directory per tab: chat/, terminal/, trace/, logs/, gateway/, health/
├── features/       One directory per data concern: chat/, gateway/, health/, sessions/,
│                   skills/, system/, tasks/, terminal/, trace/, assistant/, debuglog/,
│                   desktop/ (the Tauri-only connection notice, see below)
├── shared/         api/http.ts (the one fetch wrapper everything else uses), api/runtime.ts
│                   (isDesktopShell + apiURL/websocketURL — see below), lib/ (operatorText.ts,
│                   activityPresentation.ts — client-side counterparts of the server's
│                   model-text sanitization, see Security Model), reusable components
│                   (StatusBadge, ModuleCard, MetricRow, LogLine), assets
└── test/           setup.ts — see "the one mock-adjacent file" below (no mocks/ directory exists)
```

The same `ui/src` tree is consumed by two different shells: nginx serves it as a browser SPA (`ui/Dockerfile`), and `desktop/` packages the identical bundle in a Tauri window — not a fork, not a second copy of the components. The only place that distinction is visible in code is `shared/api/runtime.ts`: `isDesktopShell` detects the Tauri bridge at runtime, and `apiURL`/`websocketURL` resolve to the loopback orchestrator directly in that case instead of assuming a same-origin nginx proxy. Every fetch/WebSocket call site in `features/*` goes through those two helpers rather than hardcoding a relative path. See [Security Model § Desktop app isolation](../security-model.md#desktop-isolation) for what the Tauri shell can and can't do.

## Why `features/` and `pages/` are separate <a id="features-vs-pages"></a>

A `pages/*` component (e.g. `GatewayPage.tsx`) is presentation — layout, the tab's visual structure. The matching `features/*` hook (e.g. `useGatewayData.ts`) owns the actual data fetching, polling/WebSocket subscription, and mutation logic (`useGatewayRules`, `useGatewayAudit`, `useEgressMode`, and their mutators). This split means a page component reads almost like a template — it asks its feature hook for data and renders it — and the harder logic (when to refetch, how to optimistically update after a mutation, how to merge a WebSocket push into cached state) lives in one place per concern, independently testable without mounting any UI at all (see `useGatewayData.test.ts`, `usePendingSkills.test.ts`).

## State management: no global store, one hook per data source <a id="state-management"></a>

There's no Redux/Zustand/Context-based global store here — each `features/*` hook independently owns its slice of server state (usually via a combination of polling and the shared `/ws/events` WebSocket for push updates), and a page composes exactly the hooks it needs. This is a deliberate simplicity choice matching the actual complexity of the app: six largely-independent tabs, each backed by its own REST/WS concern, don't benefit much from a shared client-side store the way a deeply cross-linked single-page app would — and it avoids the specific class of bug where an unrelated tab's state update triggers a re-render somewhere it shouldn't.

## The one "mock"-adjacent file — and what it deliberately doesn't cover <a id="legitimate-mocks"></a>

**`ui/src/test/setup.ts`** contains two jsdom environment shims (`scrollIntoView`, `ResizeObserver`) for browser APIs that are absent in some test environments. They do not replace Yummi components. Individual unit tests may still script browser/network boundaries such as `fetch`; the component or hook being tested remains real.

`TerminalPanel.tsx` currently has no dedicated jsdom test. Vitest disables CSS processing centrally (`css: false`), which is enough to keep xterm's stylesheet out of unrelated tests; no xterm shim is registered in `setup.ts`.
