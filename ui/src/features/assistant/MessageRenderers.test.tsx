import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { AgentTask, ExecutionRecord } from '../../entities/types'
import { ExecutionMessage, TargetFirstLauncher, TaskSummaryMessage } from './MessageRenderers'

describe('ExecutionMessage', () => {
  const baseExecution: ExecutionRecord = {
    id: 'exec-1',
    session_id: 'session-123',
    command: 'nmap -sV -sC -p 1-1000 198.51.100.10',
    status: 'completed',
    output: 'Starting Nmap 7.94SVN\nNmap scan report for 198.51.100.10\nHost is up.'
  }

  it('renders completed command summary row visibly and collapsed by default', () => {
    render(<ExecutionMessage content="" execution={baseExecution} />)

    // The command and status should be visible in the summary
    expect(screen.getByText('nmap -sV -sC -p 1-1000 198.51.100.10')).toBeTruthy()
    const summaryBtn = screen.getByRole('button')
    expect(summaryBtn).toBeTruthy()

    // By default, completed execution output is collapsed
    expect(screen.queryByText(/Starting Nmap 7.94SVN/)).toBeNull()
  })

  it('toggles detail visibility on click', () => {
    render(<ExecutionMessage content="" execution={baseExecution} />)

    const summaryBtn = screen.getByRole('button')
    expect(screen.queryByText(/Starting Nmap 7.94SVN/)).toBeNull()

    // Click to expand
    fireEvent.click(summaryBtn)
    expect(screen.getByText(/Starting Nmap 7.94SVN/)).toBeTruthy()

    // Click to collapse
    fireEvent.click(summaryBtn)
    expect(screen.queryByText(/Starting Nmap 7.94SVN/)).toBeNull()
  })

  it('toggles detail visibility on Enter and Space keypress', () => {
    render(<ExecutionMessage content="" execution={baseExecution} />)

    const summaryBtn = screen.getByRole('button')

    // Press Enter to expand
    fireEvent.keyDown(summaryBtn, { key: 'Enter' })
    expect(screen.getByText(/Starting Nmap 7.94SVN/)).toBeTruthy()

    // Press Space to collapse
    fireEvent.keyDown(summaryBtn, { key: ' ' })
    expect(screen.queryByText(/Starting Nmap 7.94SVN/)).toBeNull()
  })

  it('renders running execution open by default with live indicator', () => {
    const runningExecution: ExecutionRecord = {
      ...baseExecution,
      id: 'exec-running',
      status: 'running',
      elapsed_secs: 14
    }

    render(
      <ExecutionMessage
        content=""
        execution={runningExecution}
        liveOutput="Connecting to target..."
      />
    )

    // Live output should be rendered without needing a click
    expect(screen.getByText(/Connecting to target.../)).toBeTruthy()
  })
})

describe('TaskSummaryMessage', () => {
  const completedTask: AgentTask = {
    id: 'task-test-1',
    session_id: 'session-123',
    goal: 'Compromise target 198.51.100.10',
    status: 'completed',
    steps_used: 72,
    step_budget: 15,
    summary: 'Successfully compromised target and retrieved user and root flags.',
    findings: [
      'Port 80/tcp: Apache 2.4.49 vulnerable to CVE-2021-41773',
      'user.txt content: user_flag{apache_rce_test}',
      'root.txt content: root_flag{suid_privesc_test}'
    ],
    created_at: 1700000000,
    updated_at: 1700001000
  }

  it('renders completed task summary card with steps, summary, and findings', () => {
    render(
      <TaskSummaryMessage
        task={completedTask}
        content=""
        timestamp={new Date(1700001000 * 1000).toISOString()}
      />
    )

    expect(screen.getByText(/Successfully compromised target/)).toBeTruthy()
    expect(screen.getByText(/72\/15/)).toBeTruthy()
    expect(screen.getByText(/Port 80\/tcp/)).toBeTruthy()
    expect(screen.getByText('user_flag{apache_rce_test}')).toBeTruthy()
    expect(screen.getByText('root_flag{suid_privesc_test}')).toBeTruthy()
    expect(screen.getByText('CVE-2021-41773')).toBeTruthy()
  })

  it('handles flag copy interaction on click', () => {
    render(
      <TaskSummaryMessage
        task={completedTask}
        content=""
        timestamp={new Date(1700001000 * 1000).toISOString()}
      />
    )

    const flagButtons = screen.getAllByRole('button')
    expect(flagButtons.length).toBeGreaterThan(0)
    fireEvent.click(flagButtons[0])
  })

  it('renders failed task summary with error note', () => {
    const failedTask: AgentTask = {
      ...completedTask,
      id: 'task-failed-1',
      status: 'failed',
      last_error: 'Connection timed out while reaching target 198.51.100.10'
    }

    render(
      <TaskSummaryMessage
        task={failedTask}
        content=""
        timestamp={new Date(1700001000 * 1000).toISOString()}
      />
    )

    expect(screen.getByText(/Connection timed out/)).toBeTruthy()
  })
})

describe('TargetFirstLauncher', () => {
  it('renders with no mode selected by default and allows toggling', () => {
    let launchedGoal = ''
    render(<TargetFirstLauncher onLaunch={(g) => { launchedGoal = g }} />)

    const buttons = screen.getAllByRole('radio')
    expect(buttons.length).toBe(2)

    // Neither button should be active/selected by default
    expect(buttons[0].getAttribute('aria-checked')).toBe('false')
    expect(buttons[0].classList.contains('is-active')).toBe(false)
    expect(buttons[1].getAttribute('aria-checked')).toBe('false')
    expect(buttons[1].classList.contains('is-active')).toBe(false)

    // Clicking Bug Bounty selects it
    fireEvent.click(buttons[0])
    expect(buttons[0].getAttribute('aria-checked')).toBe('true')
    expect(buttons[0].classList.contains('is-active')).toBe(true)
    expect(buttons[1].getAttribute('aria-checked')).toBe('false')

    // Clicking Bug Bounty again deselects it
    fireEvent.click(buttons[0])
    expect(buttons[0].getAttribute('aria-checked')).toBe('false')
    expect(buttons[0].classList.contains('is-active')).toBe(false)

    // Launching with no mode selected uses general goal
    const input = screen.getByRole('textbox')
    fireEvent.change(input, { target: { value: 'target.local' } })
    const startBtn = screen.getByRole('button', { name: /Başlat|Start/ })
    fireEvent.click(startBtn)
    expect(launchedGoal).toContain('target.local')

    // Selecting Web Pentest and launching uses specialized goal
    fireEvent.click(buttons[1])
    expect(buttons[1].getAttribute('aria-checked')).toBe('true')
    expect(buttons[1].classList.contains('is-active')).toBe(true)
    fireEvent.click(startBtn)
    expect(launchedGoal).toContain('target.local')
    expect(launchedGoal).toContain('web pentest')
  })
})

