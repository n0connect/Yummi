← [Back to README](../../README.md) · [Docs index](../README.md)

# Module: CLI & TUI (Go, root module)

The `yummi` binary is the root Go module (`go.mod`, `github.com/n0connect/yummi`) — deliberately separate from `orchestrator/`'s own module (see [Architecture § Build ecosystems](../architecture.md#build-ecosystems)). It has exactly one job: give an operator a way to control and talk to the orchestrator without touching Docker Compose or curl directly.

## Table of contents

- [Package map](#package-map)
- [The TUI is a real second front-end, not a subset](#real-frontend)
- [Why this module has no dependency on orchestrator's code](#no-orchestrator-dep)

---

## Package map <a id="package-map"></a>

| Package | Owns |
|---|---|
| **`cmd/yummi`** | The binary entry point |
| **`internal/cli`** | Flag parsing (`--tui`, `--start`, `--stop`, `--status`, `--doctor`, `--version`), Docker lifecycle orchestration for `--start`/`--stop` (`cli_unix.go`/`cli_windows.go` split for OS-specific process handling), browser launching for the default no-subcommand behavior, colored terminal output (`color.go`) |
| **`internal/tui`** | The `bubbletea`-based terminal operator console — `app.go` (the Elm-architecture `model`/`Update`/`View`), `api.go` (a REST client hitting the exact same orchestrator endpoints the web UI uses), `ws.go` (the live activity-timeline WebSocket connection), `render.go`/`styles.go` (layout and `lipgloss` styling), `session_persist.go` (remembers your last-used session across TUI restarts) |
| **`internal/config`** | Loads `~/.yummi/config.yaml` |
| **`internal/version`** | Build metadata — semver/git SHA/build date, injected via `-ldflags` at build time (see the `LDFLAGS` variable in the root `Makefile`) |

## The TUI is a real second front-end, not a subset <a id="real-frontend"></a>

It's tempting to assume a terminal UI is a stripped-down fallback for when a browser isn't available. `internal/tui` is not that — it talks to the **same** REST/WS API surface the React UI does (see [API Reference](../api-reference.md)), supports the same core operator loop (send a goal, watch the live activity timeline, approve/deny gated commands and threat-scan holds), and is built on `bubbletea`'s proper Elm-style architecture (a `model` struct, a pure `Update` function reacting to typed messages, a `View` function rendering from state) rather than being an ad-hoc script wrapping `curl`. Reach for it specifically when you're already in a terminal — over SSH to a remote box running Yummi, for instance — and don't want to also keep a browser tab open just to approve the occasional gated command.

## Why this module has no dependency on orchestrator's code <a id="no-orchestrator-dep"></a>

Both the CLI/TUI and orchestrator could theoretically live in one Go module and share types directly — a `ChatResponse` struct defined once, imported by both. This repository deliberately doesn't do that: `internal/tui/api.go` talks to orchestrator purely over HTTP/JSON, the same way any external client would, with its own local types decoding the wire format. This means the CLI/TUI binary has **zero build-time coupling** to orchestrator's internals — you can change an unexported field inside `orchestrator/internal/agent` without ever needing to rebuild or even think about `cmd/yummi`, because the only thing connecting them is a stable, versioned-in-practice HTTP API, exactly the same boundary a completely separate client written in a different language would have to respect.
