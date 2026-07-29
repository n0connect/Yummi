# ui — Frontend

> Quick local overview. For the full, kept-current reference (directory structure, why `features/` and `pages/` are split, state management), see [docs/modules/ui.md](../docs/modules/ui.md); for what each tab does, see [docs/usage-guide.md](../docs/usage-guide.md).

React 18 + Vite single-page application. Served by nginx on port 3000 in Docker; proxies `/api/` and `/ws/` to orchestrator at port 9080.

## Stack

- **React 18** + **TypeScript**
- **Vite** (build) + **Vitest** (tests)
- **react-router-dom v7** for client-side routing
- **xterm.js** for the interactive terminal panel
- **Design system**: Graphite & Phosphor — Geist/Geist Mono fonts, phosphor-teal accent (`#2dd4bf`), CSS custom properties

## Directory layout

```
src/
  app/            Global providers, router, App.tsx, app.css (design tokens)
  entities/       Shared TypeScript types (AgentTask, ExecutionRecord, etc.)
  features/       Self-contained feature modules
    assistant/    Chat panel — message rendering, proposal bar
    chat/          Chat event stream hook (useChatStream — WS, jittered backoff)
    tasks/        Agent task tracker (live plan steps, approval gate)
    trace/         TRACE tab — activity timeline + Findings/Tactics/Context/Skills/Roles
    gateway/       GATEWAY tab — status, rules, egress mode, tamper-evident audit log
    terminal/     xterm.js PTY panel
    debuglog/     SSE log stream panel
    events/       WebSocket event hooks (useExecutionEvents)
    health/       Health page widgets
    statusbar/    Top nav: Agent | Bash | Trace | Logs | Gateway | Health
    system/       System status cards
  pages/          Route-level page wrappers
    chat/ terminal/ trace/ logs/ gateway/ health/   route wrappers (/, /terminal, …)
  shared/
    api/          fetchJSON wrapper, WebSocket helpers
    components/   Generic UI primitives
  test/setup.ts   jsdom stubs (scrollIntoView · ResizeObserver · EventSource · xterm) — NOT app mocks
```

> There are **no mocks of app behavior**: `test/setup.ts` only stubs browser-only APIs that jsdom lacks (so xterm.js and the event streams load under Vitest). Tests exercise real components.

## Development

```bash
# Local dev server (HMR on :3000, proxies to orchestrator on :9080)
make dev-ui
# or directly:
cd ui && npm run dev

# Build for production
make _build-ui

# Tests
make _test-ui
# or:
cd ui && npm test
```

## Adding a new feature

1. Create `src/features/<name>/` with a component, optional CSS, and optional hooks
2. Export from `src/features/<name>/index.ts`
3. Wire into a page in `src/pages/` or add a nav entry in `StatusBar.tsx`
4. Use CSS custom properties from `app.css` for theming — never hardcode colors

## Design tokens (app.css)

| Token | Value | Use |
|---|---|---|
| `--bg` | `#0a0c0e` | Page background |
| `--surface` | `#111315` | Card/panel surface |
| `--border` | `#1e2124` | Borders |
| `--accent` | `#2dd4bf` | Phosphor teal — primary interactive |
| `--text` | `#e2e4e7` | Body text |
| `--text-muted` | `#6b7280` | Secondary text |
| `--green` | `#4ade80` | Success/completed |
| `--yellow` | `#fbbf24` | Warning/medium risk |
| `--orange` | `#f97316` | High risk |
| `--red` | `#f87171` | Critical/error |

## API contract

The UI communicates with orchestrator via:

| Endpoint | Method | Purpose |
|---|---|---|
| `/api/v1/chat` | POST | Send chat message, start agentic task |
| `/api/v1/chat/approve` | POST | Approve a proposal by ID |
| `/api/v1/tasks/{id}` | GET | Poll task state |
| `/api/v1/tasks/{id}/approve` | POST | Approve agent gate |
| `/ws/events` | WS | Execution + task status events |
| `/ws/terminal` | WS | PTY terminal stream |
| `/api/v1/system/logs` | SSE | Structured log stream |

## State management

- **Local React state** (`useState`) for component-local concerns
- **Custom hooks** for WebSocket subscriptions (see `features/events/useExecutionEvents.ts`)
- No global store library — keep state close to where it's used
