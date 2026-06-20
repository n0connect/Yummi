# ui — Frontend

React 18 + Vite single-page application. Served by nginx on port 3000 in Docker; proxies `/api/` and `/ws/` to n1 at port 9080.

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
    tasks/        Agent task tracker (live plan steps, approval gate)
    terminal/     xterm.js PTY panel
    debuglog/     SSE log stream panel
    events/       WebSocket event hooks (useExecutionEvents)
    health/       Health page widgets
    statusbar/    Top nav (Agent | Bash | Logs | Health)
    system/       System status cards
  pages/          Route-level page wrappers
    chat/         / → ChatPage
    terminal/     /terminal → TerminalPage
    logs/         /logs → LogsPage
    health/       /health → HealthPage
  shared/
    api/          fetchJSON wrapper, WebSocket helpers
    components/   Generic UI primitives
  test/mocks/     Vitest mocks (MSW handlers)
```

## Development

```bash
# Local dev server (HMR on :3000, proxies to n1 on :9080)
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

The UI communicates with n1 via:

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
