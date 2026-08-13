import { describe, expect, it } from 'vitest'
import type { ActivityEvent, ActivityKind } from '../../entities/types'
import { presentActivity } from './activityPresentation'

function activity(kind: ActivityKind, summary: string, detail?: string): ActivityEvent {
  return {
    id: 'activity-1',
    session_id: 'session-1',
    task_id: 'task-1',
    kind,
    summary,
    detail,
    timestamp: 1,
  }
}

describe('presentActivity', () => {
  it.each<ActivityKind>([
    'plan',
    'command',
    'command_result',
    'memory_recalled',
    'skill_applied',
    'handoff_started',
    'handoff_completed',
    'context_saved',
  ])('does not expose internal content for %s events', kind => {
    const visible = presentActivity(activity(
      kind,
      'PRIVATE_COMMAND --token secret',
      'hidden chain of thought and raw target output',
    ))

    expect(visible.summary).not.toContain('PRIVATE_COMMAND')
    expect(JSON.stringify(visible)).not.toContain('chain of thought')
  })

  it('keeps a concise operator-facing finding', () => {
    const visible = presentActivity(activity('finding', '  TLS   yapılandırması   zayıf  '))
    expect(visible.summary).toBe('TLS yapılandırması zayıf')
  })
})
