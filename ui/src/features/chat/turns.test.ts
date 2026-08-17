import { describe, expect, it } from 'vitest'
import { acceptsTurnStream } from './turns'

describe('acceptsTurnStream', () => {
  it('accepts only the active turn in the active session', () => {
    expect(acceptsTurnStream({ session_id: 's1', turn_id: 't1', done: false }, 's1', 't1')).toBe(true)
    expect(acceptsTurnStream({ session_id: 's1', turn_id: 't2', done: false }, 's1', 't1')).toBe(false)
    expect(acceptsTurnStream({ session_id: 's2', turn_id: 't1', done: false }, 's1', 't1')).toBe(false)
    expect(acceptsTurnStream({ session_id: 's1', turn_id: 't1', done: false }, 's1', null)).toBe(false)
  })
})
