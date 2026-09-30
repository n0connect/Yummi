import type { ChatStreamEvent } from '../../entities/types'

export function acceptsTurnStream(
  event: ChatStreamEvent,
  sessionID: string,
  activeTurnID: string | null
): boolean {
  return activeTurnID !== null && event.session_id === sessionID && event.turn_id === activeTurnID
}

