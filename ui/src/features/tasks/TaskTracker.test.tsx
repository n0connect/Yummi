import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { AgentTask } from '../../entities/types'
import { TaskTracker } from './TaskTracker'

describe('TaskTracker findings', () => {
  it('renders structured records and unmatched plain findings together', () => {
    const task: AgentTask = {
      id: 'task-1',
      session_id: 'session-1',
      goal: 'Test target',
      status: 'completed',
      steps_used: 1,
      step_budget: 2,
      findings: ['Open SSH port', 'Legacy-only observation'],
      finding_records: [
        {
          title: 'Open SSH port',
          verdict: 'verified',
          reason: 'Evidence matched step output.'
        }
      ]
    }

    render(
      <TaskTracker
        liveTask={task}
        onClose={() => {}}
        onApprove={() => {}}
        onStop={() => {}}
      />
    )

    expect(screen.getByText('Open SSH port')).toBeTruthy()
    expect(screen.getByText('Evidence matched step output.')).toBeTruthy()
    expect(screen.getByText('Legacy-only observation')).toBeTruthy()
    expect(screen.getByText('DOĞRULANDI')).toBeTruthy()
    expect(screen.getByText('İDDİA')).toBeTruthy()
  })
})
