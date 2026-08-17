import { useEffect, useRef } from 'react'
import type { ActivityEvent, AgentTask, ChatStreamEvent, ExecutionRecord, GatewayRule, PendingSkill } from '../../entities/types'
import { websocketURL } from '../../shared/api/runtime'

type WsEvent =
  | { type: 'execution.status'; data: ExecutionRecord }
  | { type: 'task.status'; data: AgentTask }
  | { type: 'gateway.status'; data: GatewayRule[] }
  | { type: 'agent.activity'; data: ActivityEvent }
  | { type: 'skill.status'; event: string; data: PendingSkill }
  | { type: 'chat.stream'; event: string; data: ChatStreamEvent }

type Options = {
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
  // Fired once a NEW socket opens after a prior one was up and then lost —
  // never on the very first connect of a session (the caller's own mount-time
  // fetch already covers that). Every event kind on this socket is push-only
  // and best-effort (execution.Store.publish and friends drop on a full
  // subscriber buffer — see server-side store.go), so whatever happened while
  // this client was disconnected is gone from the live feed for good unless
  // the caller re-fetches current state from REST here.
  onReconnect?: () => void
}

/**
 * Opens a single /ws/events WebSocket and dispatches execution.status and
 * task.status events to the provided callbacks. Reconnects on close.
 */
export function useChatStream({ onExecution, onTask, onGatewayRules, onActivity, onSkillEvent, onChatStream, onReconnect }: Options): void {
  // Keep refs so the effect only opens once, but callbacks are always current.
  const onExecutionRef    = useRef(onExecution)
  const onTaskRef         = useRef(onTask)
  const onGatewayRulesRef = useRef(onGatewayRules)
  const onActivityRef     = useRef(onActivity)
  const onSkillEventRef   = useRef(onSkillEvent)
  const onChatStreamRef   = useRef(onChatStream)
  const onReconnectRef    = useRef(onReconnect)
  onExecutionRef.current    = onExecution
  onTaskRef.current         = onTask
  onGatewayRulesRef.current = onGatewayRules
  onActivityRef.current     = onActivity
  onSkillEventRef.current   = onSkillEvent
  onChatStreamRef.current   = onChatStream
  onReconnectRef.current    = onReconnect

  useEffect(() => {
    let retryTimer: number | undefined
    let socket: WebSocket | null = null
    let closed = false
    let everConnected = false

    // Jittered exponential backoff: a healthy connection resets to baseDelay;
    // repeated failures grow geometrically up to maxDelay so a downed
    // orchestrator isn't hammered forever. TerminalPanel uses a separate,
    // simpler linear backoff (1.5s·attempt, capped at 10s) for its own
    // WebSocket — the two are intentionally different, not meant to match.
    const baseDelay = 500
    const maxDelay = 30_000
    let delay = baseDelay

    const connect = () => {
      socket = new WebSocket(websocketURL('/ws/events'))
      socket.onopen = () => {
        delay = baseDelay // reset the curve once connected
        if (everConnected) onReconnectRef.current?.()
        everConnected = true
      }
      socket.onmessage = evt => {
        try {
          const msg = JSON.parse(String(evt.data)) as WsEvent
          if (msg.type === 'execution.status' && msg.data) {
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
          }
        } catch {
          // ignore malformed payloads
        }
      }
      socket.onclose = () => {
        if (closed) return // unmounted — don't reconnect
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
  }, [])
}
