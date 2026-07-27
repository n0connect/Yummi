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
├── app/            App.tsx — top-level routing between the six tabs
├── entities/       types.ts — shared TypeScript types mirroring orchestrator's JSON shapes
├── pages/          One directory per tab: chat/, terminal/, trace/, logs/, gateway/, health/
├── features/       One directory per data concern: chat/, gateway/, health/, sessions/,
│                   skills/, system/, tasks/, terminal/, trace/, assistant/, debuglog/
├── shared/         api/http.ts (the one fetch wrapper everything else uses), reusable
│                   components (StatusBadge, ModuleCard, MetricRow, LogLine), assets
└── test/           setup.ts — see "the one mock-adjacent file" below (no mocks/ directory exists)
```

## Why `features/` and `pages/` are separate <a id="features-vs-pages"></a>

A `pages/*` component (e.g. `GatewayPage.tsx`) is presentation — layout, the tab's visual structure. The matching `features/*` hook (e.g. `useGatewayData.ts`) owns the actual data fetching, polling/WebSocket subscription, and mutation logic (`useGatewayRules`, `useGatewayAudit`, `useEgressMode`, and their mutators). This split means a page component reads almost like a template — it asks its feature hook for data and renders it — and the harder logic (when to refetch, how to optimistically update after a mutation, how to merge a WebSocket push into cached state) lives in one place per concern, independently testable without mounting any UI at all (see `useGatewayData.test.ts`, `usePendingSkills.test.ts`).

## State management: no global store, one hook per data source <a id="state-management"></a>

There's no Redux/Zustand/Context-based global store here — each `features/*` hook independently owns its slice of server state (usually via a combination of polling and the shared `/ws/events` WebSocket for push updates), and a page composes exactly the hooks it needs. This is a deliberate simplicity choice matching the actual complexity of the app: six largely-independent tabs, each backed by its own REST/WS concern, don't benefit much from a shared client-side store the way a deeply cross-linked single-page app would — and it avoids the specific class of bug where an unrelated tab's state update triggers a re-render somewhere it shouldn't.

## The one "mock"-adjacent file — and what it deliberately doesn't cover <a id="legitimate-mocks"></a>

This project has a strict, repository-wide ban on mocking its own components (see [Development § Running a single test](../development.md#single-test)). The only file in `ui/` that touches "mock" territory is **`ui/src/test/setup.ts`**, and even it isn't a mock of Yummi's own code — it's two jsdom environment stubs (`scrollIntoView`, `ResizeObserver`) for browser APIs jsdom doesn't implement natively, so components that call them don't throw during a test run. It exists to make `npm test` (vitest) able to run in Node's jsdom environment at all, not to fake any of Yummi's own logic — deleting it breaks the suite for reasons that have nothing to do with whether the app's code is correct.

**`@xterm/xterm`** (the terminal-emulator library backing the Bash tab's `TerminalPanel.tsx`) needs no stub at all, for a much simpler reason than "someone wrote a mock for it": `TerminalPanel.tsx` has **no test file** — there is no `TerminalPanel.test.tsx` anywhere in the tree, so nothing ever imports or exercises it under jsdom in the first place. The one xterm-adjacent snag that *does* need handling is its CSS import (`@xterm/xterm`'s own stylesheet), and that's resolved centrally in `ui/vitest.config.ts` via `css: false` — a one-line config flag that skips CSS imports across every test, not a per-library mock. `setup.ts`'s own trailing comment underscores the project's actual policy here rather than leaving it implicit: *"No mocks allowed. `EventSource` tests must run against real E2E environment or be designed without mocks."*
