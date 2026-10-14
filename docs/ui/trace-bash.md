# Trace and Bash workbench

Trace uses equal-height cards (340 px desktop, 320 px mobile). Card headings
and counts remain outside the independently scrollable content region. Regions
are keyboard-focusable; lists no longer introduce a second vertical scrollbar.
Findings occupies a normal grid cell instead of leaving a preceding row partly
empty. Runtime capability cards remain visible while loading or unavailable.

Loading and failed reads are shown explicitly, with retry. Failed refreshes
retain previous records; aborted or older requests cannot overwrite newer ones.
Activity replay is deduplicated and bounded. Long activity descriptions wrap.

Bash is a shared sandbox `operator` tmux shell, not a separate shell for each
chat. The conversation badge is context for the briefing. Briefing visibility
is adjustable and persisted locally; hiding it gives the terminal more width.

Unmounted terminal views cannot schedule reconnects or write to disposed xterm
instances. Backend connections use a unique sandbox-local PID receipt to detach
only their own tmux client on disconnect. They do not destroy the operator shell,
running commands, or other clients. Historical clients without receipts are not
indiscriminately detached.

QA: run `qa/trace_terminal_qc.py` in the Docker sandbox as UID 1000. Default mode
checks desktop/mobile card height and content scrolling. `--terminal` checks
real tmux attachment/cleanup, retained shell PID and briefing expansion.
`--errors` uses an actually rejected session URL to verify visible API errors.
No replacement providers or mocked browser/terminal components are used.
