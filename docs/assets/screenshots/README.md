# Screenshot catalog

This directory is the capture queue for future documentation screenshots. The Markdown pages deliberately do not embed these paths until the corresponding real PNG exists, so published documentation never renders broken images.

## How to add one

1. Bring the full stack up (`make start`) and get the UI into the state described below.
2. Capture at a reasonable width (~1280px is enough — these render inline in Markdown, not full-bleed).
3. Save as PNG at the exact path listed in the table.
4. Add the image reference to the named page only after verifying the capture contains no secrets or stale UI.

## Shot list

| Path | Referenced from | What must be visible |
|---|---|---|
| `readme/hero-agent-tab.png` | [README.md](../../../README.md) | Agent tab, mid-conversation, with one command **proposal** visible (rationale + risk level + approve/reject) — this is the single image most likely to be someone's first look at the project |
| `usage-guide/agent-tab.png` | [usage-guide.md § Agent](../../usage-guide.md#tab-agent) | Same shape as the hero shot, or a task-loop plan in progress (numbered plan + a running step) |
| `usage-guide/bash-tab.png` | [usage-guide.md § Bash](../../usage-guide.md#tab-bash) | The live terminal mid-command, showing it's a real shell in the sandbox |
| `usage-guide/trace-tab.png` | [usage-guide.md § Trace](../../usage-guide.md#tab-trace) | Activity timeline with a few different icon kinds visible (◇ plan, › command, ! finding at minimum) — ideally with one nested `└` sub-agent event so the nesting is visible too |
| `usage-guide/logs-tab.png` | [usage-guide.md § Logs](../../usage-guide.md#tab-logs) | The live SSE debug log stream with a few real lines scrolled into view |
| `usage-guide/gateway-tab.png` | [usage-guide.md § Gateway](../../usage-guide.md#tab-gateway) | The whole tab: status, active-rules table (with at least one `agent`-sourced and one `manual`-sourced rule so the `source` tag column is visible), and the Block/Allow/Ask egress selector |
| `usage-guide/health-tab.png` | [usage-guide.md § Health](../../usage-guide.md#tab-health) | The time-series charts (heap, Ollama latency, goroutines) plus the module status panel |
| `usage-guide/command-approval.png` | [usage-guide.md § Approval flows](../../usage-guide.md#approval-flows) | The command-approval prompt: command text, rationale, risk level, approve/deny |
| `usage-guide/threat-scan-hold.png` | [usage-guide.md § Approval flows](../../usage-guide.md#approval-flows) | A threat-scan hold: the flagged output banner and the approve-as-is / deny / redact options |
| `usage-guide/egress-approval.png` | [usage-guide.md § Approval flows](../../usage-guide.md#approval-flows) | An Ask-mode egress approval prompt for a new destination |
| `installation/doctor-healthy.png` | [installation.md § Verifying your install](../../installation.md#verifying) | Terminal output of `make doctor` ending in `doctor: healthy` |
| `security-model/gateway-audit-rules.png` | [security-model.md § Rule sources](../../security-model.md#rule-sources) | The Gateway tab's audit log view, showing a few hash-chained rows (rule added, rule removed, hostname_blocked) |
| `agent/pending-skill-approval.png` | [skills-and-subagents.md § Auto-skill-creation](../../agent/skills-and-subagents.md#auto-skill) | The Trace tab's Skills section with a **pending** curator-drafted skill and its approve/reject controls visible |

13 shots total. `usage-guide.md` accounts for 9 of them since it's the page actually describing the UI — do those first if capturing incrementally.
