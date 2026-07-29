import type { ActivityEvent, ActivityKind } from '../../entities/types'

type PresentedActivity = {
  label: string
  summary: string
}

const LABEL: Record<ActivityKind, string> = {
  plan: 'Plan',
  command: 'Komut',
  command_result: 'Komut sonucu',
  memory_recalled: 'Oturum bağlamı',
  skill_applied: 'Çalışma yöntemi',
  handoff_started: 'Alt görev',
  handoff_completed: 'Alt görev',
  finding: 'Bulgu',
  context_saved: 'Oturum bağlamı',
  ask: 'Operatör girdisi',
  finish: 'Görev',
}

const SAFE_SUMMARY: Record<ActivityKind, string> = {
  plan: 'Görev planı güncellendi.',
  command: 'Onaylanan komut yürütülüyor.',
  command_result: 'Komut sonucu işlendi.',
  memory_recalled: 'İlgili oturum bağlamı kullanıldı.',
  skill_applied: 'Uygun çalışma yöntemi uygulandı.',
  handoff_started: 'Uzmanlaşmış bir alt görev başlatıldı.',
  handoff_completed: 'Alt görev sonucu ana çalışmaya aktarıldı.',
  finding: 'Yeni bir bulgu kaydedildi.',
  context_saved: 'Oturum bağlamı güncellendi.',
  ask: 'Devam etmek için operatör girdisi gerekiyor.',
  finish: 'Görev tamamlandı.',
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
  const fallback = SAFE_SUMMARY[event.kind] ?? 'Çalışma durumu güncellendi.'
  const mayUseSummary = event.kind === 'finding' || event.kind === 'ask' || event.kind === 'finish'

  return {
    label: LABEL[event.kind] ?? 'Aktivite',
    summary: mayUseSummary ? concise(event.summary, fallback) : fallback,
  }
}
