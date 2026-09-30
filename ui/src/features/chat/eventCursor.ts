export type EventCursor = { epoch: string | null; sequence: number }

export const EVENT_SCHEMA_VERSION = 1

type ControlHello = {
  type: 'control.hello'
  data: {
    schema_version: number
    epoch: string
    latest_sequence: number
    resync_required: boolean
  }
}

type SequencedEvent = { type: string; epoch?: string; sequence?: number }

export function eventSocketPath(sessionID: string | undefined, cursor: EventCursor): string {
  const params = new URLSearchParams()
  if (sessionID) params.set('session_id', sessionID)
  if (cursor.epoch) {
    params.set('epoch', cursor.epoch)
    params.set('after', String(cursor.sequence))
  }
  const query = params.toString()
  return query ? `/ws/events?${query}` : '/ws/events'
}

export function reduceEventCursor(
  current: EventCursor,
  message: ControlHello | SequencedEvent,
): { cursor: EventCursor; resyncRequired: boolean } {
  if (message.type === 'control.hello' && 'data' in message) {
    const hello = message.data
    const epochChanged = current.epoch !== hello.epoch
    const resyncRequired = hello.resync_required
      || hello.schema_version !== EVENT_SCHEMA_VERSION
      || (current.epoch !== null && epochChanged)
    return {
      cursor: {
        epoch: hello.epoch,
        // A first connection receives live events only, not historical replay,
        // so acknowledge the server's current head. On a recoverable reconnect
        // retain the last applied cursor while the server sends the missing
        // suffix. Epoch/schema loss instead advances to the REST snapshot head.
        sequence: resyncRequired || epochChanged ? hello.latest_sequence : current.sequence,
      },
      resyncRequired,
    }
  }
  if ('epoch' in message && message.epoch && typeof message.sequence === 'number' && message.sequence > current.sequence) {
    return { cursor: { epoch: message.epoch, sequence: message.sequence }, resyncRequired: false }
  }
  return { cursor: current, resyncRequired: false }
}
