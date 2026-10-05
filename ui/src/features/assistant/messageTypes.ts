import type { ActivityEvent, AgentTask, ExecutionRecord } from '../../entities/types'

export type MessageRole =
  | 'user'
  | 'assistant'
  | 'system'
  | 'execution'
  | 'task'
  | 'task_summary'
  | 'activity'

export type Message = {
  id: string
  role: MessageRole
  content: string
  timestamp: string
  execution?: ExecutionRecord
  taskID?: string
  task?: AgentTask
  activity?: ActivityEvent
}
