← [Back to README](../../README.md) · [Docs index](../README.md)

# Module: CLI (Go, root module)

The `yummi` binary is the root Go module (`go.mod`, `github.com/n0connect/yummi`) — deliberately separate from `orchestrator/`'s own module (see [Architecture § Build ecosystems](../architecture.md#build-ecosystems)). It has exactly one job: give an operator a way to control and talk to the orchestrator without touching Docker Compose or curl directly. Yummi is managed entirely from the web UI — this binary's own surface is Docker lifecycle (`--start`/`--stop`/`--status`), diagnostics (`--doctor`), and opening the web UI in an app-style browser window by default.

## Table of contents

- [Package map](#package-map)
- [Why this module has no dependency on orchestrator's code](#no-orchestrator-dep)

---

## Package map <a id="package-map"></a>

| Package | Owns |
|---|---|
| **`cmd/yummi`** | The binary entry point |
| **`internal/cli`** | Flag parsing (`--start`, `--stop`, `--status`, `--doctor`, `--version`), Docker lifecycle orchestration for `--start`/`--stop` (`cli_unix.go`/`cli_windows.go` split for OS-specific process handling), browser launching for the default no-subcommand behavior (`ui_launch.go` opens the web UI in an app-style Chrome/Edge/Chromium window), colored terminal output (`color.go`) |
| **`internal/config`** | Loads `~/.yummi/config.yaml` |
| **`internal/version`** | Build metadata — semver/git SHA/build date, injected via `-ldflags` at build time (see the `LDFLAGS` variable in the root `Makefile`) |

## Why this module has no dependency on orchestrator's code <a id="no-orchestrator-dep"></a>

Both the CLI and orchestrator could theoretically live in one Go module and share types directly — a `ChatResponse` struct defined once, imported by both. This repository deliberately doesn't do that: `internal/cli/ui_launch.go`'s readiness probe talks to orchestrator purely over HTTP/JSON, the same way any external client (including the browser-based web UI) would, decoding just the fields it needs from the wire format. This means the CLI binary has **zero build-time coupling** to orchestrator's internals — you can change an unexported field inside `orchestrator/internal/agent` without ever needing to rebuild or even think about `cmd/yummi`, because the only thing connecting them is a stable, versioned-in-practice HTTP API, exactly the same boundary a completely separate client written in a different language would have to respect.
