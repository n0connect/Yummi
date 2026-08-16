import type { ActivityEvent, ExecutionRecord } from '../../entities/types'

export type MessageRole =
  | 'user'
  | 'assistant'
  | 'system'
  | 'execution'
  | 'task'
  | 'activity'

export type Message = {
  id: string
  role: MessageRole
  content: string
  timestamp: string
  execution?: ExecutionRecord
  taskID?: string
  activity?: ActivityEvent
}
