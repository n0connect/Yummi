import { useCallback, useState, useEffect, useRef, useMemo } from 'react'
import type {
  ActivityEvent,
  AgentTask,
  ChatProposal,
  ChatStreamEvent,
  ExecutionRecord,
  ServerChatMessage,
  SessionSummary
} from '../../entities/types'
import { fetchJSON } from '../../shared/api/http'
import { useChatStream } from '../chat/useChatStream'
import { acceptsTurnStream } from '../chat/turns'
import { ContextBar, type ApprovalMode } from './ContextBar'
import { sanitizeOperatorText } from '../../shared/lib/operatorText'
import { t } from '../../shared/i18n/strings'
import type { Message } from './messageTypes'
import {
  WELCOME,
  TERMINAL,
  nextId,
  capMessageContent,
  freshWelcome,
  loadDefaultMessages,
  rebuildMessages,
  loadApprovalMode,
  saveApprovalMode,
  STORAGE_MSGS
} from './messageStorage'
import {
  TaskCardMessage,
  EmptyWorkspace,
  ExecutionMessage,
  TextMessage,
  StreamingMessage,
  LIVE_OUTPUT_CAP
} from './MessageRenderers'
import { MiniSandboxPreview } from './MiniSandboxPreview'

// ≈4 chars per token (BPE average — same estimate ContextBar.tsx already
// uses for its overall-usage display; this is a per-turn size, not exact,
// since /api/v1/chat returns no real token count from Ollama).
function estimateTokens(chars: number): number {
  return Math.max(0, Math.round(chars / 4))
}

// ─── Types ────────────────────────────────────────────────────────────────────

type ChatResponse = {
  reply?: string
  session_id?: string
  turn_id?: string
  approval_required_count?: number
  data?: {
    intent?: string
    message?: string
    proposals?: ChatProposal[]
    execution?: ExecutionRecord
    confidence?: number
    task_id?: string
    task_goal?: string
  }
}

// ─── Component ───────────────────────────────────────────────────────────────

type AssistantPanelProps = {
  // sessionID is owned by ChatPage so the session rail can switch it. "default"
  // is the legacy ad-hoc bucket (sessionStorage-backed); any other value is a
  // real, server-persisted session whose history we hydrate.
  sessionID: string
  onSessionChange: (sessionID: string) => void
  // onSessionsChanged signals the rail to refresh its list (new/renamed session).
  onSessionsChanged: () => void
  // Incremented by the session rail's compact "Yeni" action.
  newChatRequest?: number
}

export function AssistantPanel({
  sessionID,
  onSessionChange,
  onSessionsChanged,
  newChatRequest = 0
}: AssistantPanelProps): JSX.Element {
  const [messages, setMessages] = useState<Message[]>(loadDefaultMessages)
  // Accordion state for execution rows: exactly one execution card is open
  // at a time, always defaulting to the most recently dispatched command —
  // the operator asked to stop having to manually re-collapse a finished
  // command's output every time the agent fires the next one. Tracks the
  // open execution's id (not an index) so it survives message-list
  // reordering/insertion; an explicit operator click can still re-target it
  // (collapse the latest, or reopen an older one) via onToggle below.
  const [openExecutionId, setOpenExecutionId] = useState<string | null>(null)
  // Execution ids already present in `messages`, tracked OUTSIDE React state
  // so upsertExecutionMessage can check/update membership synchronously.
  // Reading a `let` flag set inside a setMessages(prev => ...) updater right
  // after calling setMessages is unsound: React does not guarantee that
  // updater runs before the next line executes (it's queued and folded at
  // render time), so the "is this a new execution?" check could read stale
  // state — live-observed as the accordion sometimes never advancing to a
  // genuinely new command. A plain ref makes the check immediate and
  // deterministic regardless of React's batching/scheduling.
  const knownExecutionIdsRef = useRef<Set<string>>(new Set())
  const [input, setInput] = useState('')
  const [proposals, setProposals] = useState<ChatProposal[]>([])
  const [isLoading, setIsLoading] = useState(false)
  // streamingText holds the raw display tokens accumulated so far for the
  // in-flight /api/v1/chat call, rendered as a growing in-progress assistant
  // bubble (see onChatStream below). null = no stream in progress; '' = a
  // stream has started but no delta has arrived yet (shows a typing
  // indicator, same as ExecutionMessage's live state). Always cleared once
  // the REST response resolves — that response, not this WS-driven text, is
  // the authoritative source for the final message.
  const [streamingText, setStreamingText] = useState<string | null>(null)
  const activeTurnIDRef = useRef<string | null>(null)
  // Aborts the in-flight /api/v1/chat call (sendChat OR analyzeOutput,
  // whichever is currently running — only one can be at once, gated by
  // isLoading) when the operator clicks Stop. A stale controller from a
  // turn that already finished is never aborted by a later click: each
  // caller only aborts what it itself stored here, checked via the turnID
  // guard alongside it.
  const chatAbortRef = useRef<AbortController | null>(null)
  // Small always-visible summary of how the LAST turn ended — done, an
  // error surfaced (was previously swallowed silently by analyzeOutput's
  // catch — live-observed as "the model just stops with zero explanation"
  // after a large tool output like linpeas.sh triggered a chat-analysis
  // failure), or stopped by the operator. Distinct from isLoading/the
  // typing dots: those only say a request is IN FLIGHT, never what
  // happened once it isn't. tokens is the same ~4-chars-per-token estimate
  // ContextBar already uses — real Ollama token counts aren't returned by
  // /api/v1/chat, so this is honestly an estimate, not exact.
  const [turnStatus, setTurnStatus] = useState<{
    kind: 'done' | 'stopped' | 'error'
    tokens: number
  } | null>(null)
  // activeTask tracks the task the user is watching. Cleared when terminal.
  const [activeTask, setActiveTask] = useState<AgentTask | null>(null)
  // subTasks tracks every currently-live sub-agent task delegated (via
  // [HANDOFF]) from activeTask, keyed by task id. Handoff is depth-capped at
  // 1 (a sub-agent cannot itself delegate further), but a SINGLE step can fan
  // out to several specialists at once (MaxHandoffsPerStep, default 4) that
  // run concurrently — so this must track a set, not a single slot.
  const [subTasks, setSubTasks] = useState<Record<string, AgentTask>>({})
  // Keep snapshots independently of the currently-running task. A terminal
  // task remains a useful, inspectable part of the transcript instead of
  // vanishing the moment its final websocket event arrives.
  const [taskSnapshots, setTaskSnapshots] = useState<Record<string, AgentTask>>(
    {}
  )
  const [taskActivities, setTaskActivities] = useState<
    Record<string, ActivityEvent[]>
  >({})
  // egressHolds tracks executions held in gateway Ask mode awaiting operator
  // approval of their external target. Rendered as a pending-approval bar.
  const [egressHolds, setEgressHolds] = useState<ExecutionRecord[]>([])
  // threatHolds contain target-controlled output that matched the content
  // scanner. Unlike a banner-only warning, these records cannot be fed back to
  // the conductor until the operator explicitly releases them.
  const [threatHolds, setThreatHolds] = useState<ExecutionRecord[]>([])
  // liveOutputs accumulates each running execution's streamed output chunks
  // (WS "execution.output" events — see execution.Store.publishOutputChunk
  // server-side), keyed by execution id, so ExecutionMessage can render a
  // growing live view instead of only a static "running" ticker. Purely a
  // display aid, same discipline as streamingText for chat: the canonical
  // Output field on the terminal record always supersedes this once the
  // execution finishes, and a dropped WS stream just leaves the view frozen
  // at its last chunk rather than hanging — the terminal record still lands
  // over the same subscription (or a page reload) either way.
  const [liveOutputs, setLiveOutputs] = useState<Record<string, string>>({})
  const [approvalMode, setApprovalModeState] =
    useState<ApprovalMode>(loadApprovalMode)
  // needsTitleRef marks a freshly-created session whose name should be set from
  // the operator's first message (Claude-style auto-title).
  const needsTitleRef = useRef(false)
  // historyError is set when hydrating a real session's messages fails, so the
  // UI can show a distinct "load failed, retry" banner instead of silently
  // presenting an empty conversation that looks identical to a genuinely new
  // session (the prior bug: any transient fetch failure wiped the thread to
  // the welcome message with no indication anything went wrong).
  const [historyError, setHistoryError] = useState(false)
  const [historyLoading, setHistoryLoading] = useState(false)

  const messagesEndRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)

  // ── Persistence / hydration ──────────────────────────────────────────────────

  // Hydrate the thread when the active session changes: real sessions load
  // their persisted turns from the server; the "default" bucket falls back to
  // its sessionStorage copy. Runs on mount and on every rail switch.
  const loadHistory = useCallback((sid: string) => {
    const controller = new AbortController()
    // Reset every session-scoped transient state synchronously, before the
    // network round-trip — not after it resolves. Otherwise a stale proposal
    // or active task from the PREVIOUS session stays live for the duration of
    // the fetch; if approval mode is "auto", the auto-approve effect below can
    // fire on that stale proposal after `sessionID` has already switched,
    // running an old command under the new session's id.
    setProposals([])
    setActiveTask(null)
    setSubTasks({})
    setTaskSnapshots({})
    setTaskActivities({})
    setEgressHolds([])
    setThreatHolds([])
    setTurnStatus(null)
    setLiveOutputs({})
    // Same reasoning: the accordion tracker is session-scoped too — a stale
    // entry from the previous session is harmless (UUIDs never collide) but
    // clearing it keeps this reset block an honest, complete list of what a
    // session switch resets, matching every other line here.
    knownExecutionIdsRef.current = new Set()
    setOpenExecutionId(null)
    // isLoading isn't session-scoped state, so it must be cleared here too:
    // an in-flight request from the previous session now skips its own
    // `finally` (sessionIDRef no longer matches), so nothing else would ever
    // clear it and the new session's input would look permanently disabled.
    setIsLoading(false)
    // Same reasoning for the streaming bubble: an in-flight request's deltas
    // are about to start being filtered out by the new sessionID anyway (see
    // onChatStream), but the stale partial text must not linger on screen.
    setStreamingText(null)
    activeTurnIDRef.current = null
    autoFeedbackCountRef.current = 0
    analyzedExecutionIDsRef.current = new Set()
    if (sid === 'default') {
      setMessages(loadDefaultMessages())
      setHistoryError(false)
      setHistoryLoading(false)
      return controller
    }
    setHistoryLoading(true)
    Promise.all([
      fetchJSON<ServerChatMessage[]>(`/api/v1/sessions/${sid}/messages`, {
        signal: controller.signal
      }),
      fetchJSON<ActivityEvent[]>(`/api/v1/sessions/${sid}/activity?limit=200`, {
        signal: controller.signal
      }).catch(() => []),
      fetchJSON<ExecutionRecord[]>(
        `/api/v1/sessions/${sid}/executions?limit=100`,
        { signal: controller.signal }
      ).catch(() => [])
    ])
      .then(([rows, activities, executions]) => {
        // Activity and execution history are observability aids: an older
        // server may not expose them, but that must never block chat history.
        const hydratedExecutions: Message[] = executions
          .slice()
          .reverse()
          .map((record) => ({
            id: nextId(),
            role: 'execution',
            content: record.output || record.error || '',
            timestamp: new Date((record.created_at ?? 0) * 1000).toISOString(),
            execution: record
          }))
        setMessages(
          [...rebuildMessages(rows ?? []), ...hydratedExecutions].slice(-120)
        )
        // Seed the new-execution tracker with everything just hydrated (so a
        // later WS update to one of these doesn't get misread as "new" and
        // steal the open card back), and default the accordion to the most
        // recently dispatched one of them — the session-switch/reload
        // counterpart to upsertExecutionMessage's own live-arrival behavior.
        // `executions` is newest-first (hydratedExecutions reverses it for
        // chronological append order above), so executions[0] is the latest.
        knownExecutionIdsRef.current = new Set(executions.map((r) => r.id))
        setOpenExecutionId(executions[0]?.id ?? null)
        // Re-derive the egress/threat approval bars from the same fetch —
        // both are otherwise populated ONLY by live onExecution WS events
        // (see below), so a command left holding at the gateway (Ask-mode
        // egress) or the content scanner (threat hold) went invisible the
        // moment the operator switched away and back to this session: the
        // execution is still genuinely stuck fail-closed server-side, just
        // with no way left on screen to resolve it.
        setEgressHolds(executions.filter((r) => r.status === 'awaiting_egress_approval'))
        setThreatHolds(executions.filter((r) => r.status === 'awaiting_threat_approval'))
        setTaskActivities(
          activities.reduce<Record<string, ActivityEvent[]>>(
            (byTask, event) => {
              byTask[event.task_id] = [...(byTask[event.task_id] ?? []), event]
              return byTask
            },
            {}
          )
        )
        setHistoryError(false)
      })
      .catch((err) => {
        if (err?.name === 'AbortError') return
        // Do NOT overwrite the thread on failure — that previously made a
        // transient network error indistinguishable from "history is gone".
        // Surface a retryable banner instead (see historyError below).
        setHistoryError(true)
      })
      .finally(() => setHistoryLoading(false))
    return controller
  }, [])

  // Hydrate the thread when the active session changes: real sessions load
  // their persisted turns from the server; the "default" bucket falls back to
  // its sessionStorage copy. Runs on mount and on every rail switch.
  useEffect(() => {
    const controller = loadHistory(sessionID)
    return () => controller.abort()
  }, [sessionID, loadHistory])

  // Only the ad-hoc "default" bucket mirrors to sessionStorage; real sessions
  // are durably persisted server-side by the chat handler.
  useEffect(() => {
    if (sessionID !== 'default') return
    sessionStorage.setItem(STORAGE_MSGS, JSON.stringify(messages.slice(-120)))
  }, [messages, sessionID])

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages, activeTask, streamingText])

  useEffect(() => {
    inputRef.current?.focus()
  }, [])

  // Sync with the orchestrator's actual mode once on mount — it's the source
  // of truth (governs the agent task loop), localStorage is just a UI cache.
  useEffect(() => {
    fetchJSON<{ mode: ApprovalMode }>('/api/v1/agent/mode')
      .then((res) => {
        if (res.mode === 'auto' || res.mode === 'ask_permission') {
          setApprovalModeState(res.mode)
          saveApprovalMode(res.mode)
        }
      })
      .catch(() => {
        /* backend unreachable — keep local cache */
      })
  }, [])

  // ── Context metrics ────────────────────────────────────────────────────────

  const charCount = useMemo(
    () => messages.reduce((acc, m) => acc + m.content.length, 0),
    [messages]
  )

  // ── Message helpers ────────────────────────────────────────────────────────

  const appendMessage = useCallback(
    (msg: Omit<Message, 'id' | 'timestamp'>) => {
      const visibleContent =
        msg.role === 'assistant'
          ? sanitizeOperatorText(msg.content)
          : capMessageContent(msg.content)
      setMessages((prev) =>
        [
          ...prev,
          {
            ...msg,
            content: visibleContent,
            id: nextId(),
            timestamp: new Date().toISOString()
          }
        ].slice(-120)
      )
    },
    []
  )

  // One execution gets one transcript card. Later websocket lifecycle events
  // replace that card's data in place, making queued → running → output feel
  // like a continuous operation rather than three disconnected messages.
  const upsertExecutionMessage = useCallback((record: ExecutionRecord) => {
    // Checked/updated synchronously, BEFORE setMessages — see
    // knownExecutionIdsRef's own doc comment for why this can't live inside
    // the setMessages updater.
    const isNewExecution = !knownExecutionIdsRef.current.has(record.id)
    knownExecutionIdsRef.current.add(record.id)

    setMessages((prev) => {
      const at = prev.findIndex(
        (msg) => msg.role === 'execution' && msg.execution?.id === record.id
      )
      if (at >= 0) {
        return prev.map((msg, index) =>
          index === at
            ? {
                ...msg,
                execution: record,
                content: record.output || record.error || msg.content
              }
            : msg
        )
      }
      const next: Message = {
        id: nextId(),
        role: 'execution',
        content: record.output || record.error || '',
        timestamp: new Date().toISOString(),
        execution: record
      }
      return [...prev, next].slice(-120)
    })
    // Only a genuinely NEW execution id takes over the one open card — a
    // status update (queued -> running -> completed) on an id already on
    // screen must never reopen it if the operator (or a newer command)
    // already moved the open card elsewhere.
    if (isNewExecution) setOpenExecutionId(record.id)
  }, [])

  // cancelExecution stops a single in-flight command dispatched outside an
  // agent task (the plain chat-proposal path — approve a suggested command,
  // no plan, no activeTask). stopActiveTask below only ever had a task to
  // stop; a bare running/queued execution had no cancel affordance in the UI
  // at all even though the backend has always supported it (POST
  // /api/v1/executions/{id}/cancel, execution.Store.Cancel). The returned
  // record is pushed straight through upsertExecutionMessage, same as any
  // other execution lifecycle update from the WS stream.
  const cancelExecution = useCallback(
    async (executionID: string) => {
      try {
        const record = await fetchJSON<ExecutionRecord>(
          `/api/v1/executions/${executionID}/cancel`,
          { method: 'POST' }
        )
        upsertExecutionMessage(record)
      } catch {
        appendMessage({ role: 'system', content: t('assistant.executionCancelFailed') })
      }
    },
    [upsertExecutionMessage, appendMessage]
  )

  // newChat starts a fresh, server-persisted conversation. It creates a real
  // session (so its turns are durable and it appears in the rail) and lets the
  // first operator message auto-title it. Falls back to the ad-hoc "default"
  // bucket if session creation fails (e.g. orchestrator unreachable).
  const newChat = useCallback(async () => {
    setMessages([freshWelcome()])
    setProposals([])
    setActiveTask(null)
    setSubTasks({})
    setTaskSnapshots({})
    setTaskActivities({})
    setEgressHolds([])
    setThreatHolds([])
    setTurnStatus(null)
    setLiveOutputs({})
    // Same reasoning: the accordion tracker is session-scoped too — a stale
    // entry from the previous session is harmless (UUIDs never collide) but
    // clearing it keeps this reset block an honest, complete list of what a
    // session switch resets, matching every other line here.
    knownExecutionIdsRef.current = new Set()
    setOpenExecutionId(null)
    try {
      const s = await fetchJSON<SessionSummary>('/api/v1/sessions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: t('assistant.newSessionDefaultName') })
      })
      needsTitleRef.current = true
      onSessionChange(s.id)
      onSessionsChanged()
    } catch {
      needsTitleRef.current = false
      onSessionChange('default')
    }
  }, [onSessionChange, onSessionsChanged])

  const handledNewChatRequestRef = useRef(newChatRequest)
  useEffect(() => {
    if (newChatRequest === handledNewChatRequestRef.current) return
    handledNewChatRequestRef.current = newChatRequest
    void newChat()
  }, [newChatRequest, newChat])

  // ── WebSocket stream ───────────────────────────────────────────────────────

  const activeTaskRef = useRef(activeTask)
  activeTaskRef.current = activeTask
  // Always holds the currently-active session id. Async chat/proposal/analyze
  // calls below capture the session they were issued for and check this ref
  // before touching component state — an in-flight request that resolves
  // after the operator has switched to a different conversation must not
  // paint its (now stale) result into whatever session is on screen.
  const sessionIDRef = useRef(sessionID)
  sessionIDRef.current = sessionID

  // resyncActiveTask re-derives activeTask/subTasks/taskSnapshots for `sid`
  // from GET /sessions/{sid}/tasks — the REST-backed counterpart to the
  // WS-pushed onTask handler below. Necessary because task state is
  // otherwise reconstructed PURELY from live WS events keyed off a task id
  // the client already knows about (see onTask's `task.id !== active.id`
  // guard, driven only by locally-created tasks): switching to a session
  // with an in-progress task, or reconnecting after a dropped socket (every
  // event on this ws is best-effort — see execution.Store.publish
  // server-side), otherwise leaves the client with no way to learn that
  // task exists at all, let alone its current status. Best-effort itself: a
  // failed fetch here just means the next real WS event, if one arrives,
  // still self-heals the same way it always did.
  // Deliberately NOT skipped for sid === 'default': unlike chat messages
  // (sessionStorage-only for that bucket, see loadHistory), agent.Runner
  // never special-cases the session id when creating a task — a task
  // started from the default bucket is a real, persisted row server-side
  // under session_id="default" (HandleListTasks has no such guard either).
  // Skipping the resync here left any such task permanently unrecoverable
  // after a tab switch, dropped socket, or reload — still fully alive and
  // possibly awaiting operator approval server-side, but invisible in the UI.
  const resyncActiveTask = useCallback((sid: string) => {
    fetchJSON<AgentTask[]>(`/api/v1/sessions/${sid}/tasks`)
      .then((tasks) => {
        if (sessionIDRef.current !== sid) return
        setTaskSnapshots((prev) => {
          const next = { ...prev }
          for (const task of tasks) next[task.id] = task
          return next
        })
        const live = tasks.find((task) => !TERMINAL.has(task.status) && !task.parent_task_id)
        setActiveTask(live ?? null)
        const children = live
          ? tasks.filter((task) => task.parent_task_id === live.id)
          : []
        setSubTasks(Object.fromEntries(children.map((c) => [c.id, c])))
      })
      .catch(() => {
        /* best-effort resync; a later WS event, if any, still self-heals */
      })
  }, [])

  // resyncExecutionHolds re-derives the egress/threat approval bars for
  // `sid` from GET /sessions/{sid}/executions — the WS-reconnect
  // counterpart to loadHistory's own fetch of the same endpoint (which
  // already covers the session-switch case, see its .then above — except
  // for sid === 'default', where loadHistory skips the network entirely
  // and never fetches executions at all; see resyncActiveTask above for
  // why executions, like tasks, are real server-side rows under
  // session_id="default" too). Without this, a command left holding at
  // the gateway or the content scanner across a dropped/reconnecting
  // socket, tab switch, or reload has no way back onto the operator's
  // screen even though it is still genuinely stuck fail-closed server-side.
  const resyncExecutionHolds = useCallback((sid: string) => {
    fetchJSON<ExecutionRecord[]>(`/api/v1/sessions/${sid}/executions?limit=100`)
      .then((executions) => {
        if (sessionIDRef.current !== sid) return
        setEgressHolds(executions.filter((r) => r.status === 'awaiting_egress_approval'))
        setThreatHolds(executions.filter((r) => r.status === 'awaiting_threat_approval'))
      })
      .catch(() => {
        /* best-effort resync; a later WS event, if any, still self-heals */
      })
  }, [])

  // Resync once per session load/switch — loadHistory's own effect only
  // hydrates chat messages and activity, never task state (see above), and
  // for sid === 'default' skips the network entirely (sessionStorage-only
  // bucket for messages), so it never reaches its own executions fetch
  // either. resyncExecutionHolds was previously wired ONLY to the WS
  // onReconnect handler below, leaving a genuine egress/threat approval
  // hold under the default session invisible after a plain mount, tab
  // switch, or reload — not just after a dropped socket. A redundant fetch
  // here for a real session (already covered by loadHistory's own) is cheap.
  useEffect(() => {
    resyncActiveTask(sessionID)
    resyncExecutionHolds(sessionID)
  }, [sessionID, resyncActiveTask, resyncExecutionHolds])

  // Counts consecutive auto-feedbacks since the last user message.
  // Capped at 3 to break infinite retry loops.
  const autoFeedbackCountRef = useRef(0)
  const analyzedExecutionIDsRef = useRef(new Set<string>())

  // analyzeOutput feeds every execution result (success OR failure) back to
  // the conductor so the model can react: analyze output, retry on error, or
  // try a different approach. Called for ALL terminal statuses except 'canceled'.
  // Capped at 3 consecutive auto-feedbacks per user turn to prevent infinite loops.
  const analyzeOutput = useCallback(
    async (record: ExecutionRecord) => {
      autoFeedbackCountRef.current += 1
      if (autoFeedbackCountRef.current > 3) {
        appendMessage({
          role: 'system',
          content: t('assistant.autoLimitExceeded')
        })
        return
      }

      // Use the ANSI-stripped copies for anything fed back into the LLM — the
      // raw output may contain escape sequences a hostile target could use to
      // smuggle a payload past the banner scan (see entities/types.ts).
      const output = (record.sanitized_output ?? record.output ?? '').trim()
      const error = (record.sanitized_error ?? record.error ?? '').trim()

      // Content-layer fail-closed: this raw command output is about to be fed
      // back INTO the conductor LLM. It originates on a (hostile) target, so if
      // the orchestrator flagged it, prepend that banner — symmetric with the
      // agent loop, which prepends the same banner to its observations. The model
      // must treat the fenced output as untrusted DATA, never as instructions.
      const banner = (record.security_banner ?? '').trim()
      const guard = banner ? `${banner}\n\n` : ''

      let feedback: string
      if (record.status === 'failed') {
        const detail = error || output || 'exit code 1'
        feedback =
          guard +
          `${t('assistant.commandFailedTag')} \`${record.command}\`\n\n${t('assistant.errorOutputLabel')}\n\`\`\`\n${detail}\n\`\`\`\n\n` +
          t('assistant.commandFailedInstruction')
      } else {
        feedback =
          guard +
          `${t('assistant.commandDoneTag')} \`${record.command}\`\n\n${t('assistant.outputLabel')}\n\`\`\`\n${output || t('assistant.noOutput')}\n\`\`\`\n\n` +
          t('assistant.analyzeInstruction')
      }

      const sid = sessionID
      const turnID = crypto.randomUUID()
      activeTurnIDRef.current = turnID
      setIsLoading(true)
      setTurnStatus(null)
      const controller = new AbortController()
      chatAbortRef.current = controller
      setStreamingText('') // this call streams too — see onChatStream
      let replyChars = feedback.length
      try {
        const res = await fetchJSON<ChatResponse>('/api/v1/chat', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            message: feedback,
            session_id: sid,
            turn_id: turnID,
            from_feedback: true
          }),
          signal: controller.signal
        })
        // The operator may have switched to a different conversation while this
        // was in flight — its (now-stale) reply/proposals belong to `sid`, not
        // whatever session is on screen now, so discard rather than paint them
        // in. The turn is already durably saved server-side under `sid` and
        // will show up correctly if the operator switches back to it.
        if (sessionIDRef.current !== sid) return
        const reply = res.data?.message ?? res.reply ?? ''
        replyChars += reply.length
        if (reply) appendMessage({ role: 'assistant', content: reply })
        // If the model suggests new proposals after a failure, surface them.
        const newProposals = res.data?.proposals ?? []
        if (newProposals.length > 0) setProposals(newProposals)
        if (sessionIDRef.current === sid) setTurnStatus({ kind: 'done', tokens: estimateTokens(replyChars) })
      } catch (e) {
        // This used to be silently swallowed entirely — live-observed as
        // "the model just stops with zero explanation" after a large tool
        // output (e.g. linpeas.sh's own verbose scan output, tens of
        // thousands of characters) triggered a failure analyzing it: the
        // command's OWN result was visible in its execution card, but
        // whether the FOLLOW-UP analysis step succeeded was invisible.
        // Always surface which of those two very different things happened.
        if (sessionIDRef.current === sid) {
          if (e instanceof DOMException && e.name === 'AbortError') {
            appendMessage({ role: 'system', content: t('assistant.responseStopped') })
            setTurnStatus({ kind: 'stopped', tokens: estimateTokens(replyChars) })
          } else {
            appendMessage({ role: 'system', content: t('assistant.analysisFailed') })
            setTurnStatus({ kind: 'error', tokens: estimateTokens(replyChars) })
          }
        }
      } finally {
        if (sessionIDRef.current === sid) {
          setIsLoading(false)
          setStreamingText(null)
          if (activeTurnIDRef.current === turnID) activeTurnIDRef.current = null
          if (chatAbortRef.current === controller) chatAbortRef.current = null
        }
      }
    },
    [sessionID, appendMessage]
  )

  const onExecution = useCallback(
    (record: ExecutionRecord) => {
      if (record.session_id !== sessionID) return

      // A streamed output fragment (WS "execution.output") — accumulate it
      // for the live view and stop here. It carries no status change (the
      // record is still 'running'), so there's nothing else in this
      // function that applies to it.
      if (record.output_chunk) {
        setLiveOutputs((prev) => {
          const next = (prev[record.id] ?? '') + record.output_chunk
          return {
            ...prev,
            [record.id]:
              next.length > LIVE_OUTPUT_CAP ? next.slice(-LIVE_OUTPUT_CAP) : next
          }
        })
        return
      }

      upsertExecutionMessage(record)

      // Gateway Ask mode: the command is held until the operator approves the
      // external target. Surface it as a pending egress-approval card (deduped
      // by execution id) rather than a normal result message — the buttons live
      // there until the operator decides.
      if (record.status === 'awaiting_egress_approval') {
        setEgressHolds((prev) => [
          ...prev.filter((r) => r.id !== record.id),
          record
        ])
        return
      }

      // The command has already completed, but its result is untrusted target
      // content. Do not show it to the conductor or auto-analyze it until the
      // operator makes an explicit decision in the threat gate below.
      if (record.status === 'awaiting_threat_approval') {
        setThreatHolds((prev) => [
          ...prev.filter((r) => r.id !== record.id),
          record
        ])
        return
      }

      if (['completed', 'failed', 'canceled'].includes(record.status)) {
        // A resolved execution leaves the egress-hold list if it was there.
        setEgressHolds((prev) => prev.filter((r) => r.id !== record.id))
        setThreatHolds((prev) => prev.filter((r) => r.id !== record.id))
        // The terminal record's own Output/Error is now authoritative —
        // drop the provisional live buffer rather than let it linger.
        setLiveOutputs((prev) => {
          if (!(record.id in prev)) return prev
          const next = { ...prev }
          delete next[record.id]
          return next
        })
        // Feed back both completed AND failed to the conductor so the model can
        // react. Skip 'canceled' (user-initiated) and agent_task mode (the
        // agent loop handles its own feedback internally).
        if (
          record.status !== 'canceled' &&
          record.mode === 'assistant_loop' &&
          !analyzedExecutionIDsRef.current.has(record.id)
        ) {
          analyzedExecutionIDsRef.current.add(record.id)
          void analyzeOutput(record)
        }
      }
    },
    [sessionID, upsertExecutionMessage, analyzeOutput]
  )

  const onTask = useCallback(
    (task: AgentTask) => {
      const active = activeTaskRef.current
      setTaskSnapshots((prev) => ({ ...prev, [task.id]: task }))

      // A sub-agent task delegated from the one we're watching: track it
      // (keyed by its own id, alongside any concurrent siblings from the same
      // fan-out) so its pending approval can render nested under the
      // parent's card, instead of being silently dropped — it has its own
      // task ID the UI otherwise never learns about (see TaskTracker's
      // subTasks prop), and a concurrent sibling must not overwrite it.
      if (active && task.parent_task_id === active.id) {
        setSubTasks((prev) => ({ ...prev, [task.id]: task }))
        return
      }

      if (!active || task.id !== active.id) return

      setActiveTask(task)

      // When task completes or fails, inject a summary message and clear active.
      if (TERMINAL.has(task.status)) {
        const lines: string[] = []
        if (task.summary) lines.push(sanitizeOperatorText(task.summary))
        if (task.findings && task.findings.length > 0) {
          lines.push('')
          task.findings.forEach((f) =>
            lines.push(`• ${sanitizeOperatorText(f, t('taskTracker.defaultFindingRecorded'))}`)
          )
        }
        if (task.last_error && task.status !== 'completed') {
          lines.push(t('assistant.taskFailedNote'))
        }
        if (lines.length > 0) {
          appendMessage({ role: 'assistant', content: lines.join('\n') })
        }
      }

      // When paused and there's a question, surface it.
      if (task.status === 'paused' && task.question) {
        appendMessage({
          role: 'system',
          content: `${t('assistant.operatorQuestionPrefix')}${sanitizeOperatorText(task.question, t('assistant.defaultOperatorQuestion'))}`
        })
      }
    },
    [appendMessage]
  )

  const onActivity = useCallback(
    (ev: ActivityEvent) => {
      if (ev.session_id !== sessionID) return
      setTaskActivities((prev) => {
        const current = prev[ev.task_id] ?? []
        if (current.some((item) => item.id === ev.id)) return prev
        return { ...prev, [ev.task_id]: [...current, ev].slice(-80) }
      })
    },
    [sessionID]
  )

  // onChatStream appends raw display tokens to the in-progress assistant
  // bubble as they arrive. It never finalizes the bubble itself (that's
  // sendChat/analyzeOutput's job once the REST response resolves) — a
  // dropped or never-arriving `done` event (e.g. the websocket disconnects
  // mid-response) is harmless here for exactly that reason: the bubble just
  // stops growing and gets replaced wholesale once the REST reply lands.
  const onChatStream = useCallback(
    (ev: ChatStreamEvent) => {
      if (!acceptsTurnStream(ev, sessionID, activeTurnIDRef.current) || ev.done) return
      if (!ev.delta) return
      setStreamingText((prev) => (prev ?? '') + ev.delta)
    },
    [sessionID]
  )

  const onReconnect = useCallback(() => {
    resyncActiveTask(sessionIDRef.current)
    resyncExecutionHolds(sessionIDRef.current)
  }, [resyncActiveTask, resyncExecutionHolds])

  useChatStream({ sessionID, onExecution, onTask, onActivity, onChatStream, onReconnect })

  // ── Task actions ───────────────────────────────────────────────────────────

  const stopActiveTask = useCallback(async () => {
    if (!activeTask) return
    try {
      await fetchJSON(`/api/v1/tasks/${activeTask.id}/stop`, { method: 'POST' })
      appendMessage({ role: 'system', content: t('assistant.taskStopped') })
      setActiveTask(null)
      setSubTasks({})
    } catch {
      appendMessage({ role: 'system', content: t('assistant.taskStopFailed') })
    }
  }, [activeTask, appendMessage])

  // Resumes a task paused at the ask_operator node. `message` carries the
  // operator's answer (may be empty — the backend treats that as a plain
  // resume with no new guidance). The resulting task state arrives over the
  // normal WS task-update stream via onTask, same as any other loop step.
  const resumeTask = useCallback(
    async (taskID: string, message: string) => {
      try {
        await fetchJSON(`/api/v1/tasks/${taskID}/start`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ message })
        })
      } catch {
        appendMessage({ role: 'system', content: t('assistant.taskResumeFailed') })
      }
    },
    [appendMessage]
  )

  const approveTaskGate = useCallback(
    async (taskID: string, proposalID: string, approved: boolean) => {
      const sid = sessionID // the conversation this gate approval belongs to
      try {
        await fetchJSON(`/api/v1/tasks/${taskID}/approve`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            proposal_id: proposalID,
            approved,
            reason: approved ? '' : t('assistant.operatorRejectedReason')
          })
        })
      } catch {
        if (sessionIDRef.current !== sid) return
        appendMessage({ role: 'system', content: t('assistant.approvalSendFailed') })
      }
    },
    [appendMessage, sessionID]
  )

  const toggleTaskAutoApprove = useCallback(
    async (taskID: string, enable: boolean) => {
      const sid = sessionID // the conversation this task belongs to
      try {
        await fetchJSON(`/api/v1/tasks/${taskID}/auto-approve`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ enable })
        })
      } catch {
        if (sessionIDRef.current !== sid) return
        appendMessage({ role: 'system', content: t('assistant.autoApproveToggleFailed') })
      }
    },
    [appendMessage, sessionID]
  )

  const resolveEgress = useCallback(
    async (rec: ExecutionRecord, approved: boolean) => {
      // Optimistically drop the card; the resulting completed/failed event will
      // append the final result (or onExecution re-adds it if the call fails).
      const sid = sessionID // the conversation this hold belongs to
      setEgressHolds((prev) => prev.filter((r) => r.id !== rec.id))
      try {
        await fetchJSON(`/api/v1/executions/${rec.id}/resolve-egress`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ approved })
        })
        if (sessionIDRef.current !== sid) return
        appendMessage({
          role: 'system',
          content: approved
            ? `${t('assistant.egressApprovedBefore')}${rec.egress_target ?? rec.command}`
            : `${t('assistant.egressDeniedBefore')}${rec.egress_target ?? rec.command}`
        })
      } catch {
        if (sessionIDRef.current !== sid) return
        appendMessage({
          role: 'system',
          content: t('assistant.egressDecisionFailed')
        })
        setEgressHolds((prev) => [...prev.filter((r) => r.id !== rec.id), rec]) // restore
      }
    },
    [appendMessage, sessionID]
  )

  const resolveThreat = useCallback(
    async (rec: ExecutionRecord, approved: boolean) => {
      // The terminal event emitted by the API is the source of truth for the
      // final card and (only on approval) the subsequent LLM feedback.
      const sid = sessionID // the conversation this hold belongs to
      setThreatHolds((prev) => prev.filter((r) => r.id !== rec.id))
      try {
        await fetchJSON(`/api/v1/executions/${rec.id}/resolve-threat`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ approved })
        })
        if (sessionIDRef.current !== sid) return
        appendMessage({
          role: 'system',
          content: approved
            ? t('assistant.threatApproved')
            : t('assistant.threatDenied')
        })
      } catch {
        if (sessionIDRef.current !== sid) return
        appendMessage({
          role: 'system',
          content: t('assistant.threatDecisionFailed')
        })
        setThreatHolds((prev) => [...prev.filter((r) => r.id !== rec.id), rec])
      }
    },
    [appendMessage, sessionID]
  )

  const setApprovalMode = useCallback(
    async (mode: ApprovalMode) => {
      const previous = approvalMode
      setApprovalModeState(mode)
      saveApprovalMode(mode)
      try {
        await fetchJSON('/api/v1/agent/mode', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ mode })
        })
      } catch {
        setApprovalModeState(previous)
        saveApprovalMode(previous)
        appendMessage({ role: 'system', content: t('assistant.modeChangeFailed') })
      }
    },
    [approvalMode, appendMessage]
  )

  // ── Chat API ───────────────────────────────────────────────────────────────

  // overrideText lets a caller send a fully-composed message immediately
  // without a round-trip through input state first (setInput is
  // asynchronous — reading `input` right back in the same tick would see
  // the stale value). Used by the target-first recon/pentest launcher,
  // which composes its own goal string and sends it in one action rather
  // than filling the box and waiting for a second manual click.
  const sendChat = async (overrideText?: string) => {
    const text = (overrideText ?? input).trim()
    if (!text || isLoading) return

    const sid = sessionID // the conversation this turn actually belongs to
    const turnID = crypto.randomUUID()
    activeTurnIDRef.current = turnID
    autoFeedbackCountRef.current = 0 // reset retry counter on new user message
    setInput('')
    appendMessage({ role: 'user', content: text })
    setIsLoading(true)
    setTurnStatus(null)
    const controller = new AbortController()
    chatAbortRef.current = controller
    // Show the in-progress bubble immediately (typing indicator until the
    // first delta lands) — see onChatStream / the streamingText render below.
    setStreamingText('')

    // Auto-title a freshly-created session from the operator's first message
    // (Claude-style), then let the rail pick up the new name.
    if (needsTitleRef.current && sid !== 'default') {
      needsTitleRef.current = false
      const title = text.length > 40 ? text.slice(0, 40).trimEnd() + '…' : text
      void fetchJSON(`/api/v1/sessions/${sid}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: title })
      })
        .then(() => onSessionsChanged())
        .catch(() => {
          /* title is best-effort */
        })
    }

    let replyChars = 0
    try {
      const res = await fetchJSON<ChatResponse>('/api/v1/chat', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ message: text, session_id: sid, turn_id: turnID }),
        signal: controller.signal
      })

      // The operator may have switched to a different conversation while this
      // was in flight. The turn is already durably saved server-side under
      // `sid` — painting its reply/task/proposals into whatever session is
      // now on screen would misattribute them, so discard instead. It will
      // render correctly next time the operator switches back to `sid`.
      if (sessionIDRef.current !== sid) return

      if (res.session_id && res.session_id !== sid)
        onSessionChange(res.session_id)

      const reply = res.data?.message ?? res.reply ?? t('assistant.noReplyReceived')
      replyChars = text.length + reply.length
      appendMessage({ role: 'assistant', content: reply })

      const taskID = res.data?.task_id
      if (taskID) {
        // Fetch initial task state and start tracking it live.
        try {
          const task = await fetchJSON<AgentTask>(`/api/v1/tasks/${taskID}`)
          if (sessionIDRef.current !== sid) return
          setActiveTask(task)
          setTaskSnapshots((prev) => ({ ...prev, [task.id]: task }))
          setSubTasks({}) // a brand-new task can't have a stale sub-task from before
          // Inject a task card message so it appears in the message thread.
          appendMessage({
            role: 'task',
            content: task.goal,
            taskID
          })
        } catch {
          if (sessionIDRef.current === sid)
            appendMessage({
              role: 'system',
              content: t('assistant.taskStatusFetchFailed')
            })
        }
      } else {
        const newProposals = res.data?.proposals ?? []
        if (newProposals.length > 0) setProposals(newProposals)
      }
      if (sessionIDRef.current === sid) setTurnStatus({ kind: 'done', tokens: estimateTokens(replyChars) })
    } catch (e) {
      if (sessionIDRef.current === sid) {
        if (e instanceof DOMException && e.name === 'AbortError') {
          appendMessage({ role: 'system', content: t('assistant.responseStopped') })
          setTurnStatus({ kind: 'stopped', tokens: estimateTokens(replyChars) })
        } else {
          appendMessage({
            role: 'system',
            content: t('assistant.connectionError')
          })
          setTurnStatus({ kind: 'error', tokens: estimateTokens(replyChars) })
        }
      }
    } finally {
      if (sessionIDRef.current === sid) {
        setIsLoading(false)
        // The REST response (already handled above, success or failure) is
        // the authoritative source for the final message — the in-progress
        // bubble's job ends here regardless of whether any/all/no deltas
        // ever arrived over the websocket.
        setStreamingText(null)
        if (activeTurnIDRef.current === turnID) activeTurnIDRef.current = null
        if (chatAbortRef.current === controller) chatAbortRef.current = null
        inputRef.current?.focus()
      }
    }
  }

  // ── Proposal actions ───────────────────────────────────────────────────────

  const approveProposal = useCallback(
    async (p: ChatProposal) => {
      if (!p.id || isLoading) return
      const sid = sessionID // the conversation this proposal was created under
      setIsLoading(true)
      try {
        await fetchJSON<ChatResponse>('/api/v1/chat/approve', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ session_id: sid, proposal_id: p.id })
        })
        // If the operator switched conversations while this call was in
        // flight, the command has already been enqueued server-side under
        // `sid` — its progress/result will surface via execution.status events
        // filtered by session_id (see onExecution above), not by anything we'd
        // do here. Just skip the local proposal/message bookkeeping for a view
        // that no longer belongs to `sid`.
        if (sessionIDRef.current !== sid) return
        setProposals((prev) => prev.filter((x) => x.id !== p.id))
        appendMessage({ role: 'system', content: `${t('assistant.commandExecutedBefore')}${p.command}` })
      } catch {
        if (sessionIDRef.current === sid)
          appendMessage({ role: 'system', content: t('assistant.commandApprovalFailed') })
      } finally {
        if (sessionIDRef.current === sid) {
          setIsLoading(false)
          inputRef.current?.focus()
        }
      }
    },
    [appendMessage, isLoading, sessionID]
  )

  // Auto mode: run single-command proposals as they arrive instead of waiting
  // for a click. Ask-permission mode leaves them in the bar for the operator.
  useEffect(() => {
    if (approvalMode !== 'auto' || proposals.length === 0 || isLoading) return
    void approveProposal(proposals[0])
  }, [approvalMode, proposals, isLoading, approveProposal])

  const rejectProposal = (p: ChatProposal) =>
    setProposals((prev) => prev.filter((x) => x.id !== p.id))
  const copyCommand = (cmd: string) => {
    navigator.clipboard
      .writeText(cmd)
      .then(() =>
        appendMessage({ role: 'system', content: t('assistant.copiedToClipboard') })
      )
      .catch(() =>
        appendMessage({ role: 'system', content: t('assistant.copyFailed') })
      )
  }

  // ── Keyboard navigation ────────────────────────────────────────────────────

  const [selectedIdx, setSelectedIdx] = useState(0)
  useEffect(() => {
    setSelectedIdx(0)
  }, [proposals])

  const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Escape' && proposals.length > 0) {
      e.preventDefault()
      setProposals([])
      return
    }
    if (proposals.length > 0) {
      if (e.key === 'ArrowUp') {
        e.preventDefault()
        setSelectedIdx((i) => (i > 0 ? i - 1 : proposals.length - 1))
        return
      }
      if (e.key === 'ArrowDown') {
        e.preventDefault()
        setSelectedIdx((i) => (i < proposals.length - 1 ? i + 1 : 0))
        return
      }
      if (e.key === 'Enter') {
        e.preventDefault()
        void approveProposal(proposals[selectedIdx])
        return
      }
      if (e.key === 'c' && (e.metaKey || e.ctrlKey)) {
        e.preventDefault()
        const p = proposals[selectedIdx]
        if (p?.command) copyCommand(p.command)
        return
      }
    } else if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      void sendChat()
    }
  }

  // ── Risk color ─────────────────────────────────────────────────────────────

  const getRiskStyle = (risk?: string): React.CSSProperties => {
    const colors: Record<string, string> = {
      low: 'var(--green)',
      medium: 'var(--yellow)',
      high: 'var(--orange)',
      critical: 'var(--red)'
    }
    return { color: colors[risk ?? ''] ?? 'var(--text-muted)' }
  }

  // ── Derived state ──────────────────────────────────────────────────────────

  const taskRunning = activeTask !== null && !TERMINAL.has(activeTask.status)
  const isFreshWorkspace =
    !historyLoading &&
    !historyError &&
    !activeTask &&
    proposals.length === 0 &&
    messages.length === 1 &&
    messages[0]?.role === 'system' &&
    messages[0]?.content === WELCOME.content

  const useStarterPrompt = (prompt: string) => {
    setInput(prompt)
    requestAnimationFrame(() => inputRef.current?.focus())
  }

  // Aborts whichever chat call (sendChat or analyzeOutput) is currently in
  // flight. Both stash their own AbortController here, so this always
  // targets the real in-flight request regardless of which function
  // started it — there is deliberately no separate "stop a task" affordance
  // here; taskRunning has its own distinct UI (see the running-indicator
  // branch above) and its own cancel path (cancelExecution).
  const stopChat = () => {
    chatAbortRef.current?.abort()
  }

  // ── Render ─────────────────────────────────────────────────────────────────

  return (
    <section className="panel panel-left">
      <MiniSandboxPreview />
      <div className="panel-header">
        <span className="panel-header-title">{t('statusBar.nav.agent')}</span>
        {taskRunning && (
          <span className="running-indicator" role="status" aria-live="polite">
            <span className="loading-dot pulse" aria-hidden="true">●</span> {t('assistant.taskRunningIndicator')}
          </span>
        )}
        {!taskRunning && isLoading && (
          <span className="running-indicator" role="status" aria-live="polite">
            <span className="loading-dot pulse" aria-hidden="true">●</span> {t('assistant.replyPreparingIndicator')}
            <button
              type="button"
              className="small-button chat-stop-btn"
              onClick={stopChat}
              title={t('assistant.stopResponseButton')}
            >
              {t('assistant.stopResponseButton')}
            </button>
          </span>
        )}
        <span className="panel-header-actions">
          <button className="small-button" onClick={() => void newChat()}>
            {t('assistant.newChatButton')}
          </button>
        </span>
      </div>

      <div className="chat-area">
        {historyError && (
          <div className="history-error-banner" role="alert">
            <span>{t('assistant.historyErrorMsg')}</span>
            <button
              className="small-button"
              onClick={() => loadHistory(sessionID)}
            >
              {t('common.retry')}
            </button>
          </div>
        )}
        {historyLoading && !historyError && (
          <div className="history-loading-banner" role="status" aria-live="polite">{t('assistant.historyLoading')}</div>
        )}
        {/* Message thread. role="log" + polite/additions-only live
            announcements: new turns (assistant replies, execution results,
            task cards) are meant to be noticed without interrupting whatever
            the operator is doing, matching the ARIA "log" role's intended use
            for chat-style transcripts. aria-relevant is deliberately scoped to
            "additions" so a nested widget's own internal updates (e.g. a task
            card's progress bar) don't also re-trigger this outer region. */}
        <div
          className="chat-messages"
          role="log"
          aria-label={t('assistant.messagesLogAriaLabel')}
          aria-live="polite"
          aria-relevant="additions"
        >
          {isFreshWorkspace ? (
            <EmptyWorkspace onUsePrompt={useStarterPrompt} onLaunchTarget={(goal) => void sendChat(goal)} />
          ) : (
            messages.map((msg) => {
              if (msg.role === 'task' && msg.taskID) {
                const displayTask = taskSnapshots[msg.taskID]
                if (!displayTask) return null
                return (
                  <TaskCardMessage
                    key={msg.id}
                    task={displayTask}
                    activities={taskActivities[msg.taskID]}
                    onClose={() => {
                      setTaskSnapshots((prev) => {
                        const next = { ...prev }
                        delete next[msg.taskID!]
                        return next
                      })
                      if (activeTask?.id === msg.taskID) {
                        setActiveTask(null)
                        setSubTasks({})
                      }
                    }}
                    onApprove={(pid, approved) =>
                      approveTaskGate(msg.taskID!, pid, approved)
                    }
                    onStop={stopActiveTask}
                    onResume={(message) => resumeTask(msg.taskID!, message)}
                    subTasks={Object.values(taskSnapshots).filter(task => task.parent_task_id === msg.taskID)}
                    onApproveSub={(subTaskID, pid, approved) =>
                      approveTaskGate(subTaskID, pid, approved)
                    }
                    onToggleAutoApprove={(enable) => toggleTaskAutoApprove(msg.taskID!, enable)}
                  />
                )
              }
              if (msg.role === 'execution' && msg.execution) {
                return (
                  <ExecutionMessage
                    key={msg.id}
                    content={msg.content}
                    execution={msg.execution}
                    liveOutput={liveOutputs[msg.execution.id]}
                    onCancel={cancelExecution}
                    isOpen={openExecutionId === msg.execution.id}
                    onToggle={(open) =>
                      setOpenExecutionId(open ? msg.execution!.id : null)
                    }
                  />
                )
              }
              return (
                <TextMessage
                  key={msg.id}
                  role={msg.role}
                  content={msg.content}
                  timestamp={msg.timestamp}
                />
              )
            })
          )}
          {streamingText !== null && <StreamingMessage text={streamingText} />}
          <div ref={messagesEndRef} />
        </div>

        {/* Egress approval holds (gateway Ask mode) — an operator-blocking
            approval gate, so this is genuinely urgent: assertive, not polite. */}
        {egressHolds.length > 0 && (
          <div className="egress-bar" role="alert">
            {egressHolds.map((rec) => (
              <div key={rec.id} className="egress-card">
                <div className="egress-main">
                  <span className="egress-title">{t('assistant.egressApprovalTitle')}</span>
                  <code className="egress-target">
                    {rec.egress_target || rec.command}
                  </code>
                  <span className="egress-hint">
                    {t('assistant.egressHint')}
                  </span>
                </div>
                <div className="egress-actions">
                  <button
                    className="btn-approve"
                    onClick={() => void resolveEgress(rec, true)}
                  >
                    {t('assistant.allow')}
                  </button>
                  <button
                    className="btn-reject"
                    onClick={() => void resolveEgress(rec, false)}
                    title={t('common.reject')}
                  >
                    {t('assistant.deny')}
                  </button>
                </div>
              </div>
            ))}
          </div>
        )}

        {/* Content-layer approval holds for single-command assistant output —
            a threat-scan hold, the other genuinely urgent case: assertive. */}
        {threatHolds.length > 0 && (
          <div className="threat-bar" role="alert">
            {threatHolds.map((rec) => (
              <div key={rec.id} className="threat-card">
                <div className="threat-main">
                  <span className="threat-title">
                    {t('assistant.threatCardTitle')}
                  </span>
                  <code className="threat-command">{rec.command}</code>
                  <span className="threat-hint">
                    {rec.security_banner ||
                      t('assistant.threatDefaultHint')}
                  </span>
                  <span className="threat-operator-note">
                    {t('assistant.threatOperatorNote')}
                  </span>
                  {(rec.output || rec.error) && (
                    <pre className="threat-output">
                      {rec.output || rec.error}
                    </pre>
                  )}
                </div>
                <div className="threat-actions">
                  <button
                    className="btn-approve"
                    onClick={() => void resolveThreat(rec, true)}
                  >
                    {t('assistant.forwardToAgent')}
                  </button>
                  <button
                    className="btn-threat-reject"
                    onClick={() => void resolveThreat(rec, false)}
                    title={t('assistant.keepFromAgent')}
                  >
                    {t('assistant.keepFromAgent')}
                  </button>
                </div>
              </div>
            ))}
          </div>
        )}

        {/* Proposals */}
        {proposals.length > 0 && (
          <div className="proposals-bar">
            <div className="proposals-header">
              <span className="proposals-title">
                {proposals.length}{t('assistant.proposalsReadyAfter')}
              </span>
              <span className="proposals-hint">
                {t('assistant.proposalsHint')}
              </span>
            </div>
            <div
              className="proposals-list"
              id="assistant-proposals-listbox"
              role="listbox"
              aria-label={t('assistant.proposalsListAriaLabel')}
            >
              {proposals.map((p, idx) => (
                <div
                  key={p.id ?? idx}
                  id={`proposal-option-${idx}`}
                  role="option"
                  aria-selected={idx === selectedIdx}
                  className={`proposal-card ${idx === selectedIdx ? 'selected' : ''}`}
                  onClick={() => setSelectedIdx(idx)}
                  onDoubleClick={() => void approveProposal(p)}
                >
                  <div className="proposal-main">
                    <code className="proposal-command">$ {p.command}</code>
                    {p.rationale && (
                      <div className="proposal-rationale">
                        {sanitizeOperatorText(p.rationale)}
                      </div>
                    )}
                  </div>
                  <div className="proposal-actions">
                    <span
                      className="proposal-risk"
                      style={getRiskStyle(p.risk_level)}
                    >
                      {p.risk_level ?? '—'}
                    </span>
                    {p.target_tool && (
                      <span className="proposal-tool">{p.target_tool}</span>
                    )}
                    <button
                      className="btn-approve"
                      onClick={(e) => {
                        e.stopPropagation()
                        void approveProposal(p)
                      }}
                      disabled={isLoading}
                    >
                      {t('assistant.run')}
                    </button>
                    <button
                      className="btn-reject"
                      onClick={(e) => {
                        e.stopPropagation()
                        rejectProposal(p)
                      }}
                      title={t('common.reject')}
                      aria-label={t('common.reject')}
                    >
                      ✕
                    </button>
                    <button
                      className="btn-copy"
                      onClick={(e) => {
                        e.stopPropagation()
                        if (p.command) copyCommand(p.command)
                      }}
                      title={t('common.copy')}
                      aria-label={t('common.copy')}
                    >
                      ⎘
                    </button>
                  </div>
                </div>
              ))}
            </div>
          </div>
        )}

        {/* Input + stop/send button */}
        <div className="chat-input-area">
          <input
            ref={inputRef}
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder={
              taskRunning
                ? t('assistant.inputPlaceholderTaskRunning')
                : isLoading
                  ? t('assistant.inputPlaceholderProcessing')
                  : proposals.length > 0
                    ? t('assistant.inputPlaceholderProposalReady')
                    : t('assistant.inputPlaceholderDefault')
            }
            aria-label={t('assistant.chatInputAriaLabel')}
            // While proposals are showing, this input drives selection over
            // the listbox below via ArrowUp/ArrowDown/Enter (see
            // handleKeyDown) — aria-controls + aria-activedescendant surface
            // that same relationship to assistive tech.
            aria-controls={proposals.length > 0 ? 'assistant-proposals-listbox' : undefined}
            aria-activedescendant={proposals.length > 0 ? `proposal-option-${selectedIdx}` : undefined}
            disabled={isLoading}
          />
          {taskRunning ? (
            <button
              className="btn-stop"
              onClick={() => void stopActiveTask()}
              title={t('assistant.stopTaskTitle')}
            >
              {t('taskTracker.stop')}
            </button>
          ) : (
            <button
              onClick={() =>
                proposals.length > 0
                  ? void approveProposal(proposals[selectedIdx])
                  : void sendChat()
              }
              disabled={isLoading || (!input.trim() && proposals.length === 0)}
            >
              {proposals.length > 0 ? t('assistant.run') : t('assistant.send')}
            </button>
          )}
        </div>

        {/* Last-turn status — small and easy to miss on purpose, but ALWAYS
            present once a turn ends, whichever way it ended. Distinct from
            the typing-dots/Stop button above (those only exist while a
            request is in flight); this is the answer to "did anything
            happen after that" once it isn't. See analyzeOutput's own
            comment for the silent-failure case this replaces. */}
        {turnStatus && !isLoading && (
          <div className={`chat-turn-status chat-turn-status-${turnStatus.kind}`}>
            {turnStatus.kind === 'done' && t('assistant.turnStatusDone')}
            {turnStatus.kind === 'stopped' && t('assistant.turnStatusStopped')}
            {turnStatus.kind === 'error' && t('assistant.turnStatusError')}
            {' · ~'}
            {turnStatus.tokens >= 1000 ? `${(turnStatus.tokens / 1000).toFixed(1)}k` : turnStatus.tokens}
            {' '}
            {t('assistant.turnStatusTokensSuffix')}
          </div>
        )}

        {/* Context bar */}
        <ContextBar
          charCount={charCount}
          messageCount={messages.length}
          approvalMode={approvalMode}
          onApprovalModeChange={(mode) => void setApprovalMode(mode)}
        />
      </div>
    </section>
  )
}
