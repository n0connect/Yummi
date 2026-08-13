# Yummi Desktop

The desktop application packages the existing React operator console in a
Tauri window. It connects only to the loopback orchestrator at
`http://127.0.0.1:9080`; it does not receive Docker, shell, filesystem, or
gateway capabilities.

For the normal local workflow, start the audited runtime and native console
with one command:

```bash
make app
```

`make start` and `make desktop-dev` remain available separately when the
runtime and UI need to be restarted independently.

For a native macOS `.app` bundle, run `make desktop`. Disk-image distribution
and code signing are intentionally left to the release pipeline; the local
developer build does not invoke privileged disk-image tooling.
The browser UI remains available for container and headless deployments, while
the desktop app is the preferred local operator surface.
