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
    'command_result',
    'memory_recalled',
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

  // 'command' is deliberately NOT in the generalized list above: its
  // Summary field is the model's own one-sentence rationale ("why this
  // command, right now"), set server-side by emitPipelineActivity/
  // node_tool.go — never the raw command string or raw target output
  // (that stays in Detail, which is server-side-only and never reaches the
  // wire at all — see ActivityEvent.Detail's own doc comment). Operators
  // need this text to follow what the agent is doing step by step; see
  // presentActivity's own doc comment for the live bug this fixed.
  it('surfaces the real rationale for command events', () => {
    const visible = presentActivity(activity(
      'command',
      'Initial port scan to discover open ports and services',
      'nmap -sV -sC 198.51.100.10 (full raw tool output here)',
    ))
    expect(visible.summary).toBe('Initial port scan to discover open ports and services')
  })

  it('never leaks Detail for a command event even though Summary is shown', () => {
    const visible = presentActivity(activity(
      'command',
      'Checking for default credentials',
      'hidden chain of thought and raw target output',
    ))
    expect(JSON.stringify(visible)).not.toContain('chain of thought')
  })

  it('keeps a concise operator-facing finding', () => {
    const visible = presentActivity(activity('finding', '  TLS   yapılandırması   zayıf  '))
    expect(visible.summary).toBe('TLS yapılandırması zayıf')
  })

  // 'skill_applied' and 'skill_suggested' are the other deliberate exceptions:
  // their Summary is always a validated skill name (server-side
  // skillNamePattern, never target-controlled or model-generated prose), and
  // the whole point of splitting them into two kinds was to let an operator
  // tell "the system suggested this playbook" apart from "the model chose to
  // load this playbook" — generalizing both away would erase that distinction
  // again.
  it('surfaces the real skill name for skill_applied', () => {
    const visible = presentActivity(activity('skill_applied', 'web'))
    expect(visible.summary).toBe('web')
  })

  it('surfaces the real skill name for skill_suggested', () => {
    const visible = presentActivity(activity('skill_suggested', 'recon'))
    expect(visible.summary).toBe('recon')
  })

  it('distinguishes skill_applied from skill_suggested by label', () => {
    const applied = presentActivity(activity('skill_applied', 'web'))
    const suggested = presentActivity(activity('skill_suggested', 'web'))
    expect(applied.label).not.toBe(suggested.label)
  })

  it('never leaks Detail for a skill_applied or skill_suggested event', () => {
    const visible = presentActivity(activity('skill_applied', 'web', 'hidden chain of thought'))
    expect(JSON.stringify(visible)).not.toContain('chain of thought')
  })
})
