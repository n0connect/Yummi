import { describe, it, expect } from 'vitest'
import { sortHydratedMessages } from './messageStorage'
import type { Message } from './messageTypes'

describe('sortHydratedMessages', () => {
  it('correctly interleaves narrations (ms timestamps) and execution cards (sec timestamps)', () => {
    // Command 1 at t=100
    // Activity arrives in milliseconds: 100120 ms
    // Execution record created at 100 sec: 100000 ms
    const narration1: Message = {
      id: 'm1',
      role: 'assistant',
      content: 'Checking local IP',
      timestamp: new Date(100120).toISOString()
    }
    const exec1: Message = {
      id: 'm2',
      role: 'execution',
      content: '172.20.0.10',
      timestamp: new Date(100000).toISOString()
    }

    // Command 2 at t=105
    const narration2: Message = {
      id: 'm3',
      role: 'assistant',
      content: 'Scanning target port 80',
      timestamp: new Date(105450).toISOString()
    }
    const exec2: Message = {
      id: 'm4',
      role: 'execution',
      content: 'Apache 2.4.49',
      timestamp: new Date(105000).toISOString()
    }

    // User prompt at t=95
    const userPrompt: Message = {
      id: 'm0',
      role: 'user',
      content: 'Start pentest on target',
      timestamp: new Date(95000).toISOString()
    }

    // Shuffle them intentionally
    const shuffled = [exec2, narration1, exec1, userPrompt, narration2]
    const sorted = sortHydratedMessages(shuffled)

    expect(sorted.map(m => m.id)).toEqual(['m0', 'm1', 'm2', 'm3', 'm4'])
  })

  it('resolves ties within the same second: user prompt first, assistant second, execution third', () => {
    const timestamp = new Date(1700000000000).toISOString()
    const execMsg: Message = { id: 'exec', role: 'execution', content: 'output', timestamp }
    const userMsg: Message = { id: 'user', role: 'user', content: 'hello', timestamp }
    const assistMsg: Message = { id: 'assist', role: 'assistant', content: 'reply', timestamp }

    const sorted = sortHydratedMessages([execMsg, userMsg, assistMsg])
    expect(sorted.map(m => m.id)).toEqual(['user', 'assist', 'exec'])
  })
})
