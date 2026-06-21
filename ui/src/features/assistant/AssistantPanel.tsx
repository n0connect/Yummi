import { useCallback, useState, useEffect, useRef, useMemo } from 'react'
import type { AgentTask, ChatProposal, ExecutionRecord } from '../../entities/types'
import { fetchJSON } from '../../shared/api/http'
import { useChatStream } from '../chat/useChatStream'
import { ContextBar, type ApprovalMode } from './ContextBar'
import { TaskTracker } from '../tasks/TaskTracker'

// ─── Types ────────────────────────────────────────────────────────────────────

type ChatResponse = {
  reply?: string
  session_id?: string
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

type MessageRole = 'user' | 'assistant' | 'system' | 'execution' | 'task'

type Message = {
  id: string
  role: MessageRole
  content: string
  timestamp: string
  execution?: ExecutionRecord
  taskID?: string
}

// ─── Storage ─────────────────────────────────────────────────────────────────

const STORAGE_MSGS = 'yummi:chat:messages'
const STORAGE_SID  = 'yummi:chat:sessionId'
const STORAGE_MODE = 'yummi:agent:approvalMode'
const WELCOME: Omit<Message, 'id' | 'timestamp'> = {
  role: 'system',
  content: 'Hazırım. Hedefini ya da görevini bir cümleyle yaz.',
}

let _id = 0
const nextId = () => `msg-${++_id}`
const freshWelcome = (): Message => ({ ...WELCOME, id: nextId(), timestamp: new Date().toISOString() })

function loadMessages(): Message[] {
  try {
    const raw = sessionStorage.getItem(STORAGE_MSGS)
    if (raw) return JSON.parse(raw) as Message[]
  } catch { /* corrupted — fall through */ }
  return [freshWelcome()]
}

function loadSessionID(): string {
  return sessionStorage.getItem(STORAGE_SID) ?? 'default'
}

function loadApprovalMode(): ApprovalMode {
  try {
    return localStorage.getItem(STORAGE_MODE) === 'auto' ? 'auto' : 'ask_permission'
  } catch {
    return 'ask_permission'
  }
}

function saveApprovalMode(mode: ApprovalMode): void {
  try { localStorage.setItem(STORAGE_MODE, mode) } catch { /* unavailable — UI cache only */ }
}

// Terminal statuses where the task is no longer active.
const TERMINAL = new Set(['completed', 'failed', 'stopped', 'interrupted'])

// ─── Component ───────────────────────────────────────────────────────────────

export function AssistantPanel(): JSX.Element {
  const [messages, setMessages]   = useState<Message[]>(loadMessages)
  const [input, setInput]         = useState('')
  const [sessionID, setSessionID] = useState<string>(loadSessionID)
  const [proposals, setProposals] = useState<ChatProposal[]>([])
  const [isLoading, setIsLoading] = useState(false)
  // activeTask tracks the task the user is watching. Cleared when terminal.
  const [activeTask, setActiveTask] = useState<AgentTask | null>(null)
  const [approvalMode, setApprovalModeState] = useState<ApprovalMode>(loadApprovalMode)

  const messagesEndRef = useRef<HTMLDivElement>(null)
  const inputRef       = useRef<HTMLInputElement>(null)

  // ── Persistence ────────────────────────────────────────────────────────────

  useEffect(() => {
    sessionStorage.setItem(STORAGE_MSGS, JSON.stringify(messages.slice(-120)))
  }, [messages])

  useEffect(() => {
    sessionStorage.setItem(STORAGE_SID, sessionID)
  }, [sessionID])

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages, activeTask])

  useEffect(() => { inputRef.current?.focus() }, [])

  // Sync with the orchestrator's actual mode once on mount — it's the source
  // of truth (governs the agent task loop), localStorage is just a UI cache.
  useEffect(() => {
    fetchJSON<{ mode: ApprovalMode }>('/api/v1/agent/mode')
      .then(res => {
        if (res.mode === 'auto' || res.mode === 'ask_permission') {
          setApprovalModeState(res.mode)
          saveApprovalMode(res.mode)
        }
      })
      .catch(() => { /* backend unreachable — keep local cache */ })
  }, [])

  // ── Context metrics ────────────────────────────────────────────────────────

  const charCount = useMemo(
    () => messages.reduce((acc, m) => acc + m.content.length, 0),
    [messages]
  )

  // ── Message helpers ────────────────────────────────────────────────────────

  const appendMessage = useCallback((msg: Omit<Message, 'id' | 'timestamp'>) => {
    setMessages(prev =>
      [...prev, { ...msg, id: nextId(), timestamp: new Date().toISOString() }].slice(-120)
    )
  }, [])

  const clearChat = useCallback(() => {
    setMessages([freshWelcome()])
    setProposals([])
    setActiveTask(null)
    setSessionID('default')
    sessionStorage.removeItem(STORAGE_MSGS)
    sessionStorage.removeItem(STORAGE_SID)
  }, [])

  // ── WebSocket stream ───────────────────────────────────────────────────────

  const activeTaskRef    = useRef(activeTask)
  activeTaskRef.current  = activeTask
  // Counts consecutive auto-feedbacks since the last user message.
  // Capped at 3 to break infinite retry loops.
  const autoFeedbackCountRef = useRef(0)

  // analyzeOutput feeds every execution result (success OR failure) back to
  // the conductor so the model can react: analyze output, retry on error, or
  // try a different approach. Called for ALL terminal statuses except 'canceled'.
  // Capped at 3 consecutive auto-feedbacks per user turn to prevent infinite loops.
  const analyzeOutput = useCallback(async (record: ExecutionRecord) => {
    autoFeedbackCountRef.current += 1
    if (autoFeedbackCountRef.current > 3) {
      appendMessage({
        role: 'system',
        content: 'Otomatik deneme limiti aşıldı. Farklı bir komut denemek için yazın.',
      })
      return
    }

    const output = (record.output ?? '').trim()
    const error  = (record.error  ?? '').trim()

    let feedback: string
    if (record.status === 'failed') {
      const detail = error || output || 'exit code 1'
      feedback =
        `[KOMUT BAŞARISIZ] \`${record.command}\`\n\nHata çıktısı:\n\`\`\`\n${detail}\n\`\`\`\n\n` +
        `Komut başarısız oldu. FARKLI bir komut seç — aynı komutu tekrar deneme.`
    } else {
      feedback =
        `[KOMUT TAMAMLANDI] \`${record.command}\`\n\nÇıktı:\n\`\`\`\n${output || '(çıktı yok)'}\n\`\`\`\n\n` +
        `Bu sonucu analiz et ve kullanıcının sorusunu yanıtla.`
    }

    setIsLoading(true)
    try {
      const res = await fetchJSON<ChatResponse>('/api/v1/chat', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ message: feedback, session_id: sessionID }),
      })
      const reply = res.data?.message ?? res.reply ?? ''
      if (reply) appendMessage({ role: 'assistant', content: reply })
      // If the model suggests new proposals after a failure, surface them.
      const newProposals = res.data?.proposals ?? []
      if (newProposals.length > 0) setProposals(newProposals)
    } catch { /* silent — execution output already visible in card */ }
    finally { setIsLoading(false) }
  }, [sessionID, appendMessage])

  const onExecution = useCallback((record: ExecutionRecord) => {
    if (sessionID !== 'default' && record.session_id !== sessionID) return
    if (['completed', 'failed', 'canceled'].includes(record.status)) {
      appendMessage({
        role: 'execution',
        content: record.output || record.error || `Command ${record.status}`,
        execution: record,
      })
      // Feed back both completed AND failed to the conductor so the model can
      // react. Skip 'canceled' (user-initiated) and agent_task mode (the
      // agent loop handles its own feedback internally).
      if (record.status !== 'canceled' && record.mode === 'assistant_loop') {
        void analyzeOutput(record)
      }
    }
  }, [sessionID, appendMessage, analyzeOutput])

  const onTask = useCallback((task: AgentTask) => {
    const active = activeTaskRef.current
    if (!active || task.id !== active.id) return

    setActiveTask(task)

    // When task completes or fails, inject a summary message and clear active.
    if (TERMINAL.has(task.status)) {
      const lines: string[] = []
      if (task.summary) lines.push(task.summary)
      if (task.findings && task.findings.length > 0) {
        lines.push('')
        task.findings.forEach(f => lines.push(`• ${f}`))
      }
      if (task.last_error && task.status !== 'completed') {
        lines.push(`Hata: ${task.last_error}`)
      }
      if (lines.length > 0) {
        appendMessage({ role: 'assistant', content: lines.join('\n') })
      }
      setActiveTask(null)
    }

    // When paused and there's a question, surface it.
    if (task.status === 'paused' && task.question) {
      appendMessage({
        role: 'system',
        content: `Operatör sorusu: ${task.question}`,
      })
    }
  }, [appendMessage])

  useChatStream({ onExecution, onTask })

  // ── Task actions ───────────────────────────────────────────────────────────

  const stopActiveTask = useCallback(async () => {
    if (!activeTask) return
    try {
      await fetchJSON(`/api/v1/tasks/${activeTask.id}/stop`, { method: 'POST' })
      appendMessage({ role: 'system', content: 'Görev durduruldu.' })
      setActiveTask(null)
    } catch {
      appendMessage({ role: 'system', content: 'Görev durdurulamadı.' })
    }
  }, [activeTask, appendMessage])

  const approveTaskGate = useCallback(async (taskID: string, proposalID: string, approved: boolean) => {
    try {
      await fetchJSON(`/api/v1/tasks/${taskID}/approve`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ proposal_id: proposalID, approved, reason: approved ? '' : 'operator reddetti' }),
      })
    } catch {
      appendMessage({ role: 'system', content: 'Onay gönderilemedi.' })
    }
  }, [appendMessage])

  const setApprovalMode = useCallback(async (mode: ApprovalMode) => {
    const previous = approvalMode
    setApprovalModeState(mode)
    saveApprovalMode(mode)
    try {
      await fetchJSON('/api/v1/agent/mode', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ mode }),
      })
    } catch {
      setApprovalModeState(previous)
      saveApprovalMode(previous)
      appendMessage({ role: 'system', content: 'Mod değiştirilemedi.' })
    }
  }, [approvalMode, appendMessage])

  // ── Chat API ───────────────────────────────────────────────────────────────

  const sendChat = async () => {
    const text = input.trim()
    if (!text || isLoading) return

    autoFeedbackCountRef.current = 0  // reset retry counter on new user message
    setInput('')
    appendMessage({ role: 'user', content: text })
    setIsLoading(true)

    try {
      const res = await fetchJSON<ChatResponse>('/api/v1/chat', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ message: text, session_id: sessionID }),
      })

      if (res.session_id) setSessionID(res.session_id)

      const reply = res.data?.message ?? res.reply ?? 'Cevap alınamadı'
      appendMessage({ role: 'assistant', content: reply })

      const taskID = res.data?.task_id
      if (taskID) {
        // Fetch initial task state and start tracking it live.
        try {
          const task = await fetchJSON<AgentTask>(`/api/v1/tasks/${taskID}`)
          setActiveTask(task)
          // Inject a task card message so it appears in the message thread.
          appendMessage({
            role: 'task',
            content: task.goal,
            taskID,
          })
        } catch {
          appendMessage({ role: 'system', content: 'Görev durumu alınamadı.' })
        }
      } else {
        const newProposals = res.data?.proposals ?? []
        if (newProposals.length > 0) setProposals(newProposals)
      }
    } catch {
      appendMessage({ role: 'system', content: 'Bağlantı hatası. Orchestrator çalışıyor mu?' })
    } finally {
      setIsLoading(false)
      inputRef.current?.focus()
    }
  }

  // ── Proposal actions ───────────────────────────────────────────────────────

  const approveProposal = async (p: ChatProposal) => {
    if (!p.id || isLoading) return
    setIsLoading(true)
    try {
      await fetchJSON<ChatResponse>('/api/v1/chat/approve', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ session_id: sessionID, proposal_id: p.id }),
      })
      setProposals(prev => prev.filter(x => x.id !== p.id))
      appendMessage({ role: 'system', content: `Çalıştırıldı: ${p.command}` })
    } catch {
      appendMessage({ role: 'system', content: 'Komut onaylanamadı' })
    } finally {
      setIsLoading(false)
      inputRef.current?.focus()
    }
  }

  // Auto mode: run single-command proposals as they arrive instead of waiting
  // for a click. Ask-permission mode leaves them in the bar for the operator.
  useEffect(() => {
    if (approvalMode !== 'auto' || proposals.length === 0 || isLoading) return
    void approveProposal(proposals[0])
  }, [approvalMode, proposals, isLoading])

  const rejectProposal  = (p: ChatProposal) => setProposals(prev => prev.filter(x => x.id !== p.id))
  const copyCommand     = (cmd: string) => {
    navigator.clipboard.writeText(cmd)
      .then(() => appendMessage({ role: 'system', content: 'Panoya kopyalandı' }))
      .catch(() => appendMessage({ role: 'system', content: 'Panoya kopyalanamadı' }))
  }

  // ── Keyboard navigation ────────────────────────────────────────────────────

  const [selectedIdx, setSelectedIdx] = useState(0)
  useEffect(() => { setSelectedIdx(0) }, [proposals])

  const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Escape' && proposals.length > 0) { e.preventDefault(); setProposals([]); return }
    if (proposals.length > 0) {
      if (e.key === 'ArrowUp')   { e.preventDefault(); setSelectedIdx(i => (i > 0 ? i - 1 : proposals.length - 1)); return }
      if (e.key === 'ArrowDown') { e.preventDefault(); setSelectedIdx(i => (i < proposals.length - 1 ? i + 1 : 0)); return }
      if (e.key === 'Enter')     { e.preventDefault(); void approveProposal(proposals[selectedIdx]); return }
      if (e.key === 'c' && (e.metaKey || e.ctrlKey)) {
        e.preventDefault(); const p = proposals[selectedIdx]; if (p?.command) copyCommand(p.command); return
      }
    } else if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault(); void sendChat()
    }
  }

  // ── Risk color ─────────────────────────────────────────────────────────────

  const getRiskStyle = (risk?: string): React.CSSProperties => {
    const colors: Record<string, string> = {
      low: 'var(--green)', medium: 'var(--yellow)', high: 'var(--orange)', critical: 'var(--red)'
    }
    return { color: colors[risk ?? ''] ?? 'var(--text-muted)' }
  }

  // ── Derived state ──────────────────────────────────────────────────────────

  const taskRunning = activeTask !== null && !TERMINAL.has(activeTask.status)
  const isBlocked   = isLoading || taskRunning

  // ── Render ─────────────────────────────────────────────────────────────────

  return (
    <section className="panel panel-left">
      <div className="panel-header">
        <span className="panel-header-title">
          {isBlocked && <span className="loading-dot pulse">●</span>}
          Agent
          {taskRunning && (
            <span className="running-indicator">⚡ çalışıyor</span>
          )}
        </span>
        <span className="panel-header-actions">
          <button className="small-button" onClick={clearChat}>
            Yeni Sohbet
          </button>
        </span>
      </div>

      <div className="chat-area">
        {/* Message thread */}
        <div className="chat-messages">
          {messages.map(msg => {
            // ── Task card — inline TaskTracker ────────────────────────────
            if (msg.role === 'task' && msg.taskID) {
              const isThisActive = activeTask?.id === msg.taskID
              const displayTask  = isThisActive ? activeTask : null
              if (!displayTask) return null

              return (
                <div key={msg.id} className="msg-task-card">
                  <TaskTracker
                    liveTask={displayTask}
                    onClose={() => setActiveTask(null)}
                    onApprove={(pid, approved) => approveTaskGate(msg.taskID!, pid, approved)}
                    onStop={stopActiveTask}
                  />
                </div>
              )
            }

            // ── Execution result — collapsible ────────────────────────────
            if (msg.role === 'execution' && msg.execution) {
              return (
                <details
                  key={msg.id}
                  className="msg msg-execution"
                  open={msg.execution.status !== 'completed'}
                >
                  <summary className="execution-summary">
                    <span className="execution-chevron">›</span>
                    <span className={`execution-status ${msg.execution.status === 'completed' ? 'status-ok' : 'status-err'}`}>
                      {msg.execution.status === 'completed' ? '✓' : '✗'}
                    </span>
                    <code className="execution-cmd">{msg.execution.command}</code>
                  </summary>
                  <div className="execution-detail">
                    {msg.content && <pre className="execution-output">{msg.content}</pre>}
                    {msg.execution.error && msg.execution.status !== 'completed' && (
                      <div className="execution-error">{msg.execution.error}</div>
                    )}
                  </div>
                </details>
              )
            }

            // ── Regular text message ──────────────────────────────────────
            return (
              <div
                key={msg.id}
                className={`msg msg-${msg.role}`}
                title={new Date(msg.timestamp).toLocaleTimeString()}
              >
                {msg.role === 'user'      && <span className="msg-prefix">[USER] ❯</span>}
                {msg.role === 'assistant' && <span className="msg-prefix">[YUMMI] ❯</span>}
                {msg.role === 'system'    && <span className="msg-prefix">[SYSTEM] ❯</span>}
                <span className="msg-content" style={{ whiteSpace: 'pre-wrap' }}>{msg.content}</span>
              </div>
            )
          })}
          <div ref={messagesEndRef} />
        </div>

        {/* Proposals */}
        {proposals.length > 0 && (
          <div className="proposals-bar">
            <div className="proposals-header">
              <span className="proposals-title">{proposals.length} komut hazır</span>
              <span className="proposals-hint">↑↓ Gezin · Enter Çalıştır · Esc Kapat · ⌘C Kopyala</span>
            </div>
            <div className="proposals-list">
              {proposals.map((p, idx) => (
                <div
                  key={p.id ?? idx}
                  className={`proposal-card ${idx === selectedIdx ? 'selected' : ''}`}
                  onClick={() => setSelectedIdx(idx)}
                  onDoubleClick={() => void approveProposal(p)}
                >
                  <div className="proposal-main">
                    <code className="proposal-command">$ {p.command}</code>
                    {p.rationale && <div className="proposal-rationale">{p.rationale}</div>}
                  </div>
                  <div className="proposal-actions">
                    <span className="proposal-risk" style={getRiskStyle(p.risk_level)}>
                      {p.risk_level ?? '—'}
                    </span>
                    {p.target_tool && <span className="proposal-tool">{p.target_tool}</span>}
                    <button className="btn-approve"
                      onClick={e => { e.stopPropagation(); void approveProposal(p) }}
                      disabled={isLoading}>
                      Çalıştır
                    </button>
                    <button className="btn-reject"
                      onClick={e => { e.stopPropagation(); rejectProposal(p) }}
                      title="Reddet">✕</button>
                    <button className="btn-copy"
                      onClick={e => { e.stopPropagation(); if (p.command) copyCommand(p.command) }}
                      title="Kopyala">⎘</button>
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
            onChange={e => setInput(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder={
              taskRunning            ? 'Görev çalışıyor — durdur veya izle...'
              : isLoading            ? 'İşleniyor...'
              : proposals.length > 0 ? 'Enter ile seçili komutu çalıştır...'
              : 'Hedefini veya görevini yaz...'
            }
            disabled={isLoading}
          />
          {taskRunning ? (
            <button
              className="btn-stop"
              onClick={() => void stopActiveTask()}
              title="Görevi durdur"
            >
              ◼ Durdur
            </button>
          ) : (
            <button
              onClick={() => proposals.length > 0 ? void approveProposal(proposals[selectedIdx]) : void sendChat()}
              disabled={isLoading || (!input.trim() && proposals.length === 0)}
            >
              {proposals.length > 0 ? 'Çalıştır' : 'Gönder'}
            </button>
          )}
        </div>

        {/* Context bar */}
        <ContextBar
          charCount={charCount}
          sessionID={sessionID}
          messageCount={messages.length}
          isLoading={isBlocked}
          approvalMode={approvalMode}
          onApprovalModeChange={mode => void setApprovalMode(mode)}
        />
      </div>
    </section>
  )
}
