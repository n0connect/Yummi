import { useEffect, useRef } from 'react'
import type { ActivityEvent, AgentTask, ChatStreamEvent, ExecutionRecord, GatewayRule, PendingSkill } from '../../entities/types'
import { websocketURL } from '../../shared/api/runtime'
import { eventSocketPath, reduceEventCursor, type EventCursor } from './eventCursor'

type EventMeta = { schema_version?: number; epoch?: string; sequence?: number }
type WsEvent = EventMeta & (
  | { type: 'control.hello'; data: { schema_version: number; epoch: string; latest_sequence: number; resync_required: boolean } }
  | { type: 'execution.status'; data: ExecutionRecord }
  | { type: 'task.status'; data: AgentTask }
  | { type: 'gateway.status'; data: GatewayRule[] }
  | { type: 'agent.activity'; data: ActivityEvent }
  | { type: 'skill.status'; event: string; data: PendingSkill }
  | { type: 'chat.stream'; event: string; data: ChatStreamEvent }
  | { type: 'session.changed'; data: { id: string } }
)

export type EventConnectionState = 'connecting' | 'live' | 'recovering' | 'stale'

type Options = {
  sessionID?: string
  onExecution?: (rec: ExecutionRecord) => void
  onTask?: (task: AgentTask) => void
  onGatewayRules?: (rules: GatewayRule[]) => void
  onActivity?: (ev: ActivityEvent) => void
  // Fired whenever a curator-drafted skill is created or its status changes
  // (approved/rejected/rolled back to pending after a failed activation).
  // `event` is the backend's skillcurator.Event.Type ("skill." + status,
  // e.g. "skill.pending", "skill.approved") — most consumers can ignore it
  // and just treat any call as "the pending-skills list may have changed."
  onSkillEvent?: (skill: PendingSkill, event: string) => void
  // Fired for every raw display-token chunk (and the terminal done signal)
  // while a conductor.Engine.Chat/ChatWithHistory call streams — see
  // conductor.StreamEvent server-side. Purely cosmetic: the authoritative
  // reply still arrives over the normal /api/v1/chat REST response.
  onChatStream?: (ev: ChatStreamEvent) => void
  // Fired only when the server reports that its bounded replay window cannot
  // cover this cursor (process epoch changed or the cursor is too old). Normal
  // reconnects are healed by ordered replay and do not force a REST refresh.
  onReconnect?: () => void
  onConnectionState?: (state: EventConnectionState) => void
  onSessionsChanged?: () => void
}

/**
 * Opens a single /ws/events WebSocket and dispatches execution.status and
 * task.status events to the provided callbacks. Reconnects on close.
 */
export function useChatStream({ sessionID, onExecution, onTask, onGatewayRules, onActivity, onSkillEvent, onChatStream, onReconnect, onConnectionState, onSessionsChanged }: Options): void {
  // Keep refs so the effect only opens once, but callbacks are always current.
  const onExecutionRef    = useRef(onExecution)
  const onTaskRef         = useRef(onTask)
  const onGatewayRulesRef = useRef(onGatewayRules)
  const onActivityRef     = useRef(onActivity)
  const onSkillEventRef   = useRef(onSkillEvent)
  const onChatStreamRef   = useRef(onChatStream)
  const onReconnectRef    = useRef(onReconnect)
  const onConnectionStateRef = useRef(onConnectionState)
  const onSessionsChangedRef = useRef(onSessionsChanged)
  onExecutionRef.current    = onExecution
  onTaskRef.current         = onTask
  onGatewayRulesRef.current = onGatewayRules
  onActivityRef.current     = onActivity
  onSkillEventRef.current   = onSkillEvent
  onChatStreamRef.current   = onChatStream
  onReconnectRef.current    = onReconnect
  onConnectionStateRef.current = onConnectionState
  onSessionsChangedRef.current = onSessionsChanged

  useEffect(() => {
    let retryTimer: number | undefined
    let socket: WebSocket | null = null
    let closed = false
    let cursor: EventCursor = { epoch: null, sequence: 0 }

    // Jittered exponential backoff: a healthy connection resets to baseDelay;
    // repeated failures grow geometrically up to maxDelay so a downed
    // orchestrator isn't hammered forever. TerminalPanel uses a separate,
    // simpler linear backoff (1.5s·attempt, capped at 10s) for its own
    // WebSocket — the two are intentionally different, not meant to match.
    const baseDelay = 500
    const maxDelay = 30_000
    let delay = baseDelay

    const connect = () => {
      onConnectionStateRef.current?.(cursor.epoch ? 'recovering' : 'connecting')
      socket = new WebSocket(websocketURL(eventSocketPath(sessionID, cursor)))
      socket.onopen = () => {
        delay = baseDelay // reset the curve once connected
      }
      socket.onmessage = evt => {
        try {
          const msg = JSON.parse(String(evt.data)) as WsEvent
          const reduced = reduceEventCursor(cursor, msg)
          cursor = reduced.cursor
          if (msg.type === 'control.hello') {
            if (reduced.resyncRequired) {
              onConnectionStateRef.current?.('recovering')
              onReconnectRef.current?.()
            }
            onConnectionStateRef.current?.('live')
            onSessionsChangedRef.current?.()
          } else if (msg.type === 'execution.status' && msg.data) {
            onExecutionRef.current?.(msg.data as ExecutionRecord)
          } else if (msg.type === 'task.status' && msg.data) {
            onTaskRef.current?.(msg.data as AgentTask)
          } else if (msg.type === 'gateway.status' && msg.data) {
            onGatewayRulesRef.current?.(msg.data as GatewayRule[])
          } else if (msg.type === 'agent.activity' && msg.data) {
            onActivityRef.current?.(msg.data as ActivityEvent)
          } else if (msg.type === 'skill.status' && msg.data) {
            onSkillEventRef.current?.(msg.data, msg.event)
          } else if (msg.type === 'chat.stream' && msg.data) {
            onChatStreamRef.current?.(msg.data)
          } else if (msg.type === 'session.changed' && msg.data) {
            onSessionsChangedRef.current?.()
          }
        } catch {
          // ignore malformed payloads
        }
      }
      socket.onclose = () => {
        if (closed) return // unmounted — don't reconnect
        onConnectionStateRef.current?.(cursor.epoch ? 'recovering' : 'stale')
        const wait = delay + Math.floor(Math.random() * (delay / 5)) // +0–20% jitter
        retryTimer = window.setTimeout(connect, wait)
        delay = Math.min(delay * 2, maxDelay)
      }
    }

    connect()
    return () => {
      closed = true
      if (retryTimer !== undefined) window.clearTimeout(retryTimer)
      socket?.close()
    }
  }, [sessionID])
}
