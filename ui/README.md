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
    assistant/    Chat panel — message rendering with command rationales, proposal bar
    chat/         Chat/event stream hook (useChatStream — WS, jittered backoff)
    desktop/      Desktop panel — noVNC/websockify canvas & status (remote resize)
    operator/     Operator Trust Rail & Briefing panel
    tasks/        Agent task tracker (live plan steps, approval gate)
    trace/        TRACE tab panels:
                  - AssetTreePanel: Attack Surface Management (ASM) hierarchical tree
                  - ScopeRulesPanel: Program-scope rules (include/exclude) & live classifier
                  - FindingsPanel: Severity-aware, filterable findings matrix with PoCs
                  - ProxyBridgePanel: HTTP proxy traffic stream injection
                  - ReportExportModal: Multi-format report export (HackerOne, Bugcrowd, Exec, JSON)
    gateway/      GATEWAY tab — status, rules, egress mode, tamper-evident audit log
    terminal/     xterm.js PTY panel
    debuglog/     SSE log stream panel
    health/       Health page widgets & telemetry
    sessions/     Session rail and session mutations
    skills/       Pending-skill review hooks
    statusbar/    Top nav: Agent | Bash | Desktop | Trace | Logs | Gateway | Health + Light/Dark theme toggle
    system/       System status cards
  pages/          Route-level page wrappers
    chat/ terminal/ desktop/ trace/ logs/ gateway/ health/   route wrappers (/, /terminal, /desktop, …)
  shared/
    api/          fetchJSON wrapper, WebSocket helpers (runtime.ts, http.ts)
    components/   Generic UI primitives (YummiLogo, StatusBadge, MetricRow, LogLine)
    i18n/         Build-time string translation table (strings.ts, driven by YUMMI_LANG)
    lib/          Sanitization, presentation helpers, and useTheme.ts (Light/Dark themes)
  test/setup.ts   jsdom stubs for scrollIntoView and ResizeObserver
```

> `test/setup.ts` only supplies two browser APIs missing from jsdom. Individual tests may script network/browser boundaries, while component and hook logic remains the code under test.

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
2. Wire it into a page under `src/pages/`, or add a route/nav entry in `app/App.tsx` and `features/statusbar/StatusBar.tsx`
3. Use the semantic CSS custom properties from `app/app.css` for theming

## Design tokens (app.css) & Theme Engine

The UI supports both Dark and Light themes with automatic system preference detection and a manual toggle in the navigation bar.

| Token | Dark Value | Light Value | Use |
|---|---|---|---|
| `--bg-base` | `#0a0c0e` | `#f4f6f8` | App background |
| `--bg-primary` / `--bg-secondary` | `#0e1113` / `#14181b` | `#ffffff` / `#eaedf0` | Panel canvases and surfaces |
| `--border` | `rgba(255,255,255,0.08)` | `rgba(0,0,0,0.1)` | Borders |
| `--accent` | `#2dd4bf` | `#0d9488` | Phosphor teal — primary interactive |
| `--text-primary` | `#f4f7f6` | `#111827` | Primary text |
| `--text-secondary` / `--text-muted` | translucent | translucent | Secondary and muted text |
| `--green` / `--yellow` / `--orange` / `--red` | semantic palette | semantic palette | State, risk, and error colors |

## API contract

The UI communicates with orchestrator via:

| Endpoint | Method | Purpose |
|---|---|---|
| `/api/v1/chat` | POST | Send chat message, start agentic task |
| `/api/v1/chat/approve` | POST | Approve a proposal by ID |
| `/api/v1/tasks/{id}` | GET | Poll task state |
| `/api/v1/tasks/{id}/approve` | POST | Approve agent gate |
| `/api/v1/tasks/{id}/auto-approve` | POST | Set per-task auto-approval toggle |
| `/api/v1/sessions/{sessionID}/assets` | GET | Retrieve ASM asset hierarchy (`format=tree` or `format=flat`) |
| `/api/v1/sessions/{sessionID}/scope-rules` | GET/POST/DELETE | Manage bug-bounty in-scope/out-of-scope program rules |
| `/api/v1/sessions/{sessionID}/scope-rules/classify` | POST | Live hostname classification against program scope |
| `/api/v1/sessions/{sessionID}/report` | GET | Generate exportable reports (HackerOne, Bugcrowd, Executive, JSON) |
| `/api/v1/proxy/inject` | POST | Inject HTTP request/response flows into knowledge graph |
| `/api/v1/operator/briefing` | GET | Surfaced tactics, skills, and findings for operator |
| `/ws/events` | WS | Monotonic replayable event stream (execution, task, activity, gateway, skill, chat stream) |
| `/ws/terminal` | WS | PTY terminal stream (Bash tab) |
| `/ws/desktop` | WS | noVNC / websockify stream (Desktop tab) |
| `/api/v1/system/logs` | SSE | Structured log stream (Logs tab) |

## State management

- **Local React state** (`useState`) for component-local concerns
- **Custom hooks** for WebSocket subscriptions (see `features/events/useExecutionEvents.ts`)
- No global store library — keep state close to where it's used
