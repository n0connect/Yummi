import type { ServerChatMessage } from '../../entities/types'
import { sanitizeOperatorText } from '../../shared/lib/operatorText'
import { t } from '../../shared/i18n/strings'
import type { ApprovalMode } from './ContextBar'
import type { Message } from './messageTypes'

// ─── Storage ─────────────────────────────────────────────────────────────────
// Pure persistence/message-shaping helpers with no React and no dependency on
// AssistantPanel's session-isolation state — safe to read, test, and change
// independently of the stateful component.

// Real sessions persist server-side (chat_messages); only the legacy "default"
// ad-hoc bucket still uses sessionStorage so a quick unsaved chat survives a
// reload. STORAGE_MSGS is therefore scoped to the default bucket only.
export const STORAGE_MSGS = 'yummi:chat:messages:default'
const STORAGE_MODE = 'yummi:agent:approvalMode'
export const WELCOME: Omit<Message, 'id' | 'timestamp'> = {
  role: 'system',
  content: t('assistant.welcome')
}

let _id = 0
export const nextId = (): string => `msg-${++_id}`

// MAX_MESSAGE_CONTENT_CHARS bounds a single message's rendered/stored text.
// Execution output, task summaries, and findings all ultimately originate
// from sandbox tool output run against a (hostile) pentest target — an
// unbounded field could pressure the render tree or sessionStorage with a
// single oversized record. Mirrors the head+tail capping the orchestrator
// already applies to agent observations (orchestrator/internal/agent/memory.go).
const MAX_MESSAGE_CONTENT_CHARS = 64 * 1024

export function capMessageContent(content: string): string {
  if (content.length <= MAX_MESSAGE_CONTENT_CHARS) return content
  const edge = MAX_MESSAGE_CONTENT_CHARS / 2
  return `${content.slice(0, edge)}\n...[message capped: ${content.length} chars total, middle elided]...\n${content.slice(-edge)}`
}

export const freshWelcome = (): Message => ({
  ...WELCOME,
  id: nextId(),
  timestamp: new Date().toISOString()
})

// loadDefaultMessages restores the ad-hoc "default" bucket from sessionStorage.
export function loadDefaultMessages(): Message[] {
  try {
    const raw = sessionStorage.getItem(STORAGE_MSGS)
    if (raw) return JSON.parse(raw) as Message[]
  } catch {
    /* corrupted — fall through */
  }
  return [freshWelcome()]
}

// rebuildMessages turns persisted server turns back into thread messages.
// Reloaded turns render as plain text — any proposals they carried were
// already acted on, so they are intentionally not made re-actionable here.
export function rebuildMessages(rows: ServerChatMessage[]): Message[] {
  if (rows.length === 0) return [freshWelcome()]
  return rows.map((r) => {
    const role =
      r.role === 'user' || r.role === 'assistant' || r.role === 'system'
        ? r.role
        : 'system'
    return {
      id: nextId(),
      role,
      content:
        role === 'assistant' ? sanitizeOperatorText(r.content) : r.content,
      timestamp: new Date((r.created_at ?? 0) * 1000).toISOString()
    }
  })
}

export function loadApprovalMode(): ApprovalMode {
  try {
    return localStorage.getItem(STORAGE_MODE) === 'auto'
      ? 'auto'
      : 'ask_permission'
  } catch {
    return 'ask_permission'
  }
}

export function saveApprovalMode(mode: ApprovalMode): void {
  try {
    localStorage.setItem(STORAGE_MODE, mode)
  } catch {
    /* unavailable — UI cache only */
  }
}

// Terminal statuses where the task is no longer active.
export const TERMINAL = new Set(['completed', 'failed', 'stopped', 'interrupted'])

// sortHydratedMessages merges reconstituted history items into chronological order.
// Timestamps are aligned to whole seconds so that execution records (seconds)
// and activity events (milliseconds) align properly, with ties resolved by rank:
// goal/user prompt first, then agent words/narration, then execution cards.
export function sortHydratedMessages(messages: Message[]): Message[] {
  const rank = (m: Message) => (m.role === 'user' ? 0 : m.role === 'execution' ? 2 : 1)
  return messages.slice().sort((a, b) => {
    const secA = Math.floor(Date.parse(a.timestamp) / 1000)
    const secB = Math.floor(Date.parse(b.timestamp) / 1000)
    return (secA - secB) || (rank(a) - rank(b))
  })
}
