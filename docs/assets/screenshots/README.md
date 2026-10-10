# Screenshot catalog

This directory holds documentation screenshots. Pages only embed a path once the real PNG exists at it, so published documentation never renders a broken image.

## How to add one

1. Bring the full stack up (`make start`) and get the UI into the state described below.
2. Capture at full resolution, then resize to **1280px wide** before saving (these render inline in Markdown, not full-bleed) — e.g. `sips -Z 1280 capture.png`.
3. Save as PNG at the exact path listed in the table.
4. Add the image reference to the named page only after verifying the capture contains no secrets or stale UI.

## Captured (2026-10-10)

All six below are real captures from a live, unmodified `minimax/minimax-m2.1` run that fully compromised the two-stage CVE lab (`lab-target/`) — CVE-2021-41773 RCE, a self-opened reverse-shell terminal, SUID-python3 GTFOBins privesc, both flags — except `bash-tab.png` and `desktop-tab.png`, which are from an earlier session (same UI, same sandbox identity).

| Path | Referenced from | What's visible |
|---|---|---|
| `readme/hero-workspace.png` | [README.md](../../../README.md) | The Agent tab's empty-state workspace: goal input, target field, and the two quick-start launchers (Bug Bounty Recon, Web Pentest) |
| `readme/hero-agent-tab.png` | [agent/overview.md](../../agent/overview.md) | The completed task summary card: status pill, step count, executive summary, CVE tag, both captured flags |
| `usage-guide/agent-tab.png` | [usage-guide.md § Agent](../../usage-guide.md#tab-agent) | Mid-task: the model's plain-language narration above each dispatched command, with live status pills |
| `usage-guide/terminals-tab.png` | [usage-guide.md § Terminals](../../usage-guide.md#tab-terminals), [sandbox-execution.md § Tier 4](../../agent/sandbox-execution.md#tier-4) | A reverse shell the agent caught itself, live in the watch-only Terminals view — already escalated to root, browsing the filesystem and reading a captured flag |
| `usage-guide/bash-tab.png` | [usage-guide.md § Bash](../../usage-guide.md#tab-bash) | The operator's own Bash tab: the `yummi-pwnbox` ASCII banner and shortcut helpers |
| `usage-guide/desktop-tab.png` | [usage-guide.md § Desktop](../../usage-guide.md#tab-desktop) | The connected XFCE desktop: Burp Suite, Wireshark, SecLists, ExploitDB, Metasploit Console |
| `usage-guide/gateway-tab.png` | [usage-guide.md § Gateway](../../usage-guide.md#tab-gateway), [security-model.md § Egress modes](../../security-model.md#egress-modes) | Kernel enforcement status, the egress-mode switch (`block_all`), and the active rules table |

## Still needed

| Path | Referenced from | What must be visible |
|---|---|---|
| `usage-guide/trace-tab.png` | [usage-guide.md § Trace](../../usage-guide.md#tab-trace) | Activity timeline with a few different icon kinds visible ([*] plan, [>] command, [!] finding at minimum) — ideally with one nested sub-agent event so the nesting is visible too |
| `usage-guide/logs-tab.png` | [usage-guide.md § Logs](../../usage-guide.md#tab-logs) | The live SSE debug log stream with a few real lines scrolled into view |
| `usage-guide/health-tab.png` | [usage-guide.md § Health](../../usage-guide.md#tab-health) | The time-series charts (heap, Ollama latency, goroutines) plus the module status panel |
| `usage-guide/command-approval.png` | [usage-guide.md § Approval flows](../../usage-guide.md#approval-flows) | The command-approval prompt: command text, rationale, risk level, approve/deny — `ask_permission` mode, not yet captured (everything above is from an `auto`-mode run) |
| `usage-guide/threat-scan-hold.png` | [usage-guide.md § Approval flows](../../usage-guide.md#approval-flows) | A threat-scan hold: the flagged output banner and the approve-as-is / deny / redact options |
| `usage-guide/egress-approval.png` | [usage-guide.md § Approval flows](../../usage-guide.md#approval-flows) | An Ask-mode egress approval prompt for a new destination |
| `installation/doctor-healthy.png` | [installation.md § Verifying your install](../../installation.md#verifying) | Terminal output of `make doctor` ending in `doctor: healthy` |
| `security-model/gateway-audit-rules.png` | [security-model.md § Rule sources](../../security-model.md#rule-sources) | The Gateway tab's **Audit** sub-view specifically, showing a few hash-chained rows (rule added, rule removed, hostname_blocked) — distinct from the Active Rules table already captured above |
| `agent/pending-skill-approval.png` | [skills-and-subagents.md § Auto-skill-creation](../../agent/skills-and-subagents.md#auto-skill) | The Trace tab's Skills section with a **pending** curator-drafted skill and its approve/reject controls visible |

9 shots still needed. None of the captured six show an `ask_permission`-mode approval prompt — if capturing more, that family (command-approval, threat-scan-hold, egress-approval) is the highest-value gap, since every current screenshot is from an unattended `auto`-mode run.
