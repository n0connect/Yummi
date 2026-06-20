import { useEffect, useRef } from 'react'
import type { AgentTask, ExecutionRecord } from '../../entities/types'

const wsScheme = window.location.protocol === 'https:' ? 'wss' : 'ws'

type WsEvent =
  | { type: 'execution.status'; data: ExecutionRecord }
  | { type: 'task.status'; data: AgentTask }

type Options = {
  onExecution?: (rec: ExecutionRecord) => void
  onTask?: (task: AgentTask) => void
}

/**
 * Opens a single /ws/events WebSocket and dispatches execution.status and
 * task.status events to the provided callbacks. Reconnects on close.
 */
export function useChatStream({ onExecution, onTask }: Options): void {
  // Keep refs so the effect only opens once, but callbacks are always current.
  const onExecutionRef = useRef(onExecution)
  const onTaskRef      = useRef(onTask)
  onExecutionRef.current = onExecution
  onTaskRef.current      = onTask

  useEffect(() => {
    let retryTimer: number | undefined
    let socket: WebSocket | null = null

    const connect = () => {
      socket = new WebSocket(`${wsScheme}://${window.location.host}/ws/events`)
      socket.onmessage = evt => {
        try {
          const msg = JSON.parse(String(evt.data)) as WsEvent
          if (msg.type === 'execution.status' && msg.data) {
            onExecutionRef.current?.(msg.data as ExecutionRecord)
          } else if (msg.type === 'task.status' && msg.data) {
            onTaskRef.current?.(msg.data as AgentTask)
          }
        } catch {
          // ignore malformed payloads
        }
      }
      socket.onclose = () => {
        retryTimer = window.setTimeout(connect, 1500)
      }
    }

    connect()
    return () => {
      if (retryTimer !== undefined) window.clearTimeout(retryTimer)
      socket?.close()
    }
  }, [])
}
