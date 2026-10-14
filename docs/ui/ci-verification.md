# CI execution placement

UI validation runs ESLint, Vitest and the production build. The Docker `quality`
target also runs lint before tests, so local image validation cannot silently
miss the lint gate used by GitHub Actions.

Sandbox-MCP tests launch real commands and Chromium. GitHub Actions first builds
the real Kali sandbox image, then runs `make _test-sandbox-mcp`. Tests execute as
UID 1000, without networking, host mounts or capabilities, with a read-only root
filesystem and narrowly scoped temporary filesystems. Runtime confinement remains
enabled; tests must not bypass it or be moved onto the CI runner directly.

The first CI sandbox build can take substantially longer than subsequent runs;
BuildKit's GitHub Actions cache preserves build layers, not runtime permissions.
Provider-backed research and autonomous-model QC require a separately configured,
gateway-routed live sandbox; the isolated CI suite does not claim to cover them.
