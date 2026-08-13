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
 * vocabulary. Only explicitly user-facing event types may keep their summary;
 * commands, recalled memory, handoffs and tool results are always generalized.
 * This also protects clients rendering activity persisted by older servers.
 */
export function presentActivity(event: ActivityEvent): PresentedActivity {
  const fallback = SAFE_SUMMARY[event.kind] ?? t('activity.summary.default')
  const mayUseSummary = event.kind === 'finding' || event.kind === 'ask' || event.kind === 'finish'

  return {
    label: LABEL[event.kind] ?? t('activity.label.default'),
    summary: mayUseSummary ? concise(event.summary, fallback) : fallback,
  }
}
