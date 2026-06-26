import { useEffect, useRef } from 'react'
import type { ActivityEvent, AgentTask, ExecutionRecord, GatewayRule } from '../../entities/types'

const wsScheme = window.location.protocol === 'https:' ? 'wss' : 'ws'

type WsEvent =
  | { type: 'execution.status'; data: ExecutionRecord }
  | { type: 'task.status'; data: AgentTask }
  | { type: 'gateway.status'; data: GatewayRule[] }
  | { type: 'agent.activity'; data: ActivityEvent }

type Options = {
  onExecution?: (rec: ExecutionRecord) => void
  onTask?: (task: AgentTask) => void
  onGatewayRules?: (rules: GatewayRule[]) => void
  onActivity?: (ev: ActivityEvent) => void
}

/**
 * Opens a single /ws/events WebSocket and dispatches execution.status and
 * task.status events to the provided callbacks. Reconnects on close.
 */
export function useChatStream({ onExecution, onTask, onGatewayRules, onActivity }: Options): void {
  // Keep refs so the effect only opens once, but callbacks are always current.
  const onExecutionRef    = useRef(onExecution)
  const onTaskRef         = useRef(onTask)
  const onGatewayRulesRef = useRef(onGatewayRules)
  const onActivityRef     = useRef(onActivity)
  onExecutionRef.current    = onExecution
  onTaskRef.current         = onTask
  onGatewayRulesRef.current = onGatewayRules
  onActivityRef.current     = onActivity

  useEffect(() => {
    let retryTimer: number | undefined
    let socket: WebSocket | null = null
    let closed = false

    // Jittered exponential backoff (matches TerminalPanel): a healthy connection
    // resets to baseDelay; repeated failures grow geometrically up to maxDelay so
    // a downed orchestrator isn't hammered every 1.5s forever.
    const baseDelay = 500
    const maxDelay = 30_000
    let delay = baseDelay

    const connect = () => {
      socket = new WebSocket(`${wsScheme}://${window.location.host}/ws/events`)
      socket.onopen = () => {
        delay = baseDelay // reset the curve once connected
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
