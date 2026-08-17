import { describe, expect, it } from 'vitest'
import { eventSocketPath, reduceEventCursor } from './eventCursor'

describe('event replay cursor', () => {
  it('builds a session-scoped reconnect URL from the last acknowledged event', () => {
    expect(eventSocketPath('session one', { epoch: 'epoch-1', sequence: 42 })).toBe(
      '/ws/events?session_id=session+one&epoch=epoch-1&after=42',
    )
  })

  it('tracks envelopes and requests a REST resync only when the server cannot replay', () => {
    const initial = { epoch: null, sequence: 0 }
    const hello = reduceEventCursor(initial, {
      type: 'control.hello',
      data: { schema_version: 1, epoch: 'epoch-1', latest_sequence: 7, resync_required: false },
    })
    expect(hello).toEqual({ cursor: { epoch: 'epoch-1', sequence: 7 }, resyncRequired: false })

    const advanced = reduceEventCursor(hello.cursor, { type: 'task.status', epoch: 'epoch-1', sequence: 8 })
    expect(advanced).toEqual({ cursor: { epoch: 'epoch-1', sequence: 8 }, resyncRequired: false })

    const stale = reduceEventCursor(advanced.cursor, {
      type: 'control.hello',
      data: { schema_version: 1, epoch: 'epoch-2', latest_sequence: 12, resync_required: true },
    })
    expect(stale).toEqual({ cursor: { epoch: 'epoch-2', sequence: 12 }, resyncRequired: true })
  })

  it('requires a snapshot when the server event schema is incompatible', () => {
    const result = reduceEventCursor({ epoch: 'epoch-1', sequence: 8 }, {
      type: 'control.hello',
      data: { schema_version: 2, epoch: 'epoch-1', latest_sequence: 12, resync_required: false },
    })
    expect(result).toEqual({ cursor: { epoch: 'epoch-1', sequence: 12 }, resyncRequired: true })
  })

  it('defensively requires a snapshot when an established epoch changes', () => {
    const result = reduceEventCursor({ epoch: 'epoch-1', sequence: 8 }, {
      type: 'control.hello',
      data: { schema_version: 1, epoch: 'epoch-2', latest_sequence: 12, resync_required: false },
    })
    expect(result).toEqual({ cursor: { epoch: 'epoch-2', sequence: 12 }, resyncRequired: true })
  })
})
