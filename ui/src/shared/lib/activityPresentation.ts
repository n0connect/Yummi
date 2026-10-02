import type { ActivityEvent, ActivityKind } from '../../entities/types'
import { t } from '../i18n/strings'

type PresentedActivity = {
  label: string
  summary: string
}

const LABEL: Record<ActivityKind, string> = {
  plan: t('activity.label.plan'),
  command: t('activity.label.command'),
  command_result: t('activity.label.command_result'),
  memory_recalled: t('activity.label.memory_recalled'),
  skill_applied: t('activity.label.skill_applied'),
  handoff_started: t('activity.label.handoff_started'),
  handoff_completed: t('activity.label.handoff_completed'),
  finding: t('activity.label.finding'),
  context_saved: t('activity.label.context_saved'),
  ask: t('activity.label.ask'),
  finish: t('activity.label.finish'),
}

const SAFE_SUMMARY: Record<ActivityKind, string> = {
  plan: t('activity.summary.plan'),
  command: t('activity.summary.command'),
  command_result: t('activity.summary.command_result'),
  memory_recalled: t('activity.summary.memory_recalled'),
  skill_applied: t('activity.summary.skill_applied'),
  handoff_started: t('activity.summary.handoff_started'),
  handoff_completed: t('activity.summary.handoff_completed'),
  finding: t('activity.summary.finding'),
  context_saved: t('activity.summary.context_saved'),
  ask: t('activity.summary.ask'),
  finish: t('activity.summary.finish'),
}

function concise(value: string, fallback: string): string {
  const normalized = value.replace(/\s+/g, ' ').trim()
  if (!normalized) return fallback
  return normalized.length > 180 ? `${normalized.slice(0, 177)}…` : normalized
}

/**
 * Converts internal activity telemetry to an intentionally small, operator-safe
 * vocabulary. Most event kinds are always generalized to protect clients
 * rendering activity persisted by older servers, and because their raw
 * summary text can be uninformative filler server-side anyway.
 *
 * 'command' is the deliberate exception: its Summary field IS the model's
 * own one-sentence rationale ("why this command, right now" — see
 * execution.Record.Rationale / agent's emitPipelineActivity/node_tool.go),
 * not raw target-controlled output — that lives in the separate Detail
 * field, which stays server-side-only (json:"-", never sent over the wire;
 * see ActivityEvent.Detail's own doc comment). Generalizing it here was a
 * real, operator-reported bug: the live task-progress feed showed "Running
 * the approved command" for every single step regardless of what the agent
 * was actually doing or why, even though the real rationale was already
 * being sent and rendered correctly everywhere else (the AGENT tab's own
 * execution cards). Confirmed live: raw GET /sessions/{id}/activity already
 * carried rationale text like "Initial port scan to discover open ports and
 * services on target 198.51.100.10" in `summary` — this function was simply
 * throwing it away before the operator ever saw it.
 */
export function presentActivity(event: ActivityEvent): PresentedActivity {
  const fallback = SAFE_SUMMARY[event.kind] ?? t('activity.summary.default')
  const mayUseSummary =
    event.kind === 'finding' ||
    event.kind === 'ask' ||
    event.kind === 'finish' ||
    event.kind === 'command'

  return {
    label: LABEL[event.kind] ?? t('activity.label.default'),
    summary: mayUseSummary ? concise(event.summary, fallback) : fallback,
  }
}
