import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
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
        onResume={() => {}}
      />
    )

    expect(screen.getByText('Open SSH port')).toBeTruthy()
    expect(screen.getByText('Evidence matched step output.')).toBeTruthy()
    expect(screen.getByText('Legacy-only observation')).toBeTruthy()
    expect(screen.getByText('DOĞRULANDI')).toBeTruthy()
    expect(screen.getByText('İDDİA')).toBeTruthy()
  })
})

describe('TaskTracker paused resume', () => {
  it('lets the operator type an answer and resume the ask_operator pause', () => {
    const task: AgentTask = {
      id: 'task-2',
      session_id: 'session-1',
      goal: 'Recon target',
      status: 'paused',
      steps_used: 10,
      step_budget: 15,
      question: 'Halted after 6 consecutive command failures. Operator guidance needed.'
    }
    const onResume = vi.fn()

    render(
      <TaskTracker
        liveTask={task}
        onClose={() => {}}
        onApprove={() => {}}
        onStop={() => {}}
        onResume={onResume}
      />
    )

    const input = screen.getByPlaceholderText(
      'Soruyu yanıtlayın, ya da boş bırakıp sadece devam ettirin…'
    )
    fireEvent.change(input, { target: { value: 'Try scanme.nmap.org, I opened the gateway.' } })
    fireEvent.click(screen.getByText('Devam ettir'))

    expect(onResume).toHaveBeenCalledWith('Try scanme.nmap.org, I opened the gateway.')
  })

  it('lets the operator resume with no guidance at all (plain resume)', () => {
    const task: AgentTask = {
      id: 'task-3',
      session_id: 'session-1',
      goal: 'Recon target',
      status: 'paused',
      steps_used: 5,
      step_budget: 15
    }
    const onResume = vi.fn()

    render(
      <TaskTracker
        liveTask={task}
        onClose={() => {}}
        onApprove={() => {}}
        onStop={() => {}}
        onResume={onResume}
      />
    )

    fireEvent.click(screen.getByText('Devam ettir'))
    expect(onResume).toHaveBeenCalledWith('')
  })
})
