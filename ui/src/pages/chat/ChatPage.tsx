import { useState, useEffect, useCallback } from 'react'
import { AssistantPanel } from '../../features/assistant/AssistantPanel'
import { SessionRail } from '../../features/sessions/SessionRail'

const STORAGE_SID = 'yummi:chat:sessionId'
const STORAGE_RAIL_OPEN = 'yummi:chat:railOpen'

/**
 * Chat page shell. Owns the active session id so the collapsible session rail
 * and the assistant panel stay in sync: the rail switches sessions, the panel
 * hydrates and persists them. "default" is the legacy ad-hoc bucket.
 */
export function ChatPage(): JSX.Element {
  const [sessionID, setSessionID] = useState<string>(
    () => sessionStorage.getItem(STORAGE_SID) ?? 'default',
  )
  // Operator-reported: reloading the page while the rail was open closed it
  // every time, discarding a deliberate choice for no reason — persisted the
  // same way STORAGE_SID already is, so a reload restores the whole
  // workspace layout, not just which session is active.
  const [railOpen, setRailOpen] = useState<boolean>(() => {
    try {
      return localStorage.getItem(STORAGE_RAIL_OPEN) === '1'
    } catch {
      return false
    }
  })
  const [newChatRequest, setNewChatRequest] = useState(0)
  // Bumped whenever a session is created/renamed so the rail refetches its list.
  const [refreshKey, setRefreshKey] = useState(0)

  useEffect(() => {
    sessionStorage.setItem(STORAGE_SID, sessionID)
  }, [sessionID])

  useEffect(() => {
    try {
      localStorage.setItem(STORAGE_RAIL_OPEN, railOpen ? '1' : '0')
    } catch {
      // ignore storage errors (private mode, quota, etc.) — the rail still
      // works for this page view, it just won't persist across a reload
    }
  }, [railOpen])

  const bumpSessions = useCallback(() => setRefreshKey(k => k + 1), [])

  const selectSession = useCallback((id: string) => {
    setSessionID(id)
    setRailOpen(false)
  }, [])

  return (
    <main className={`chat-page ${railOpen ? 'rail-open' : 'rail-closed'}`}>
      <SessionRail
        open={railOpen}
        onToggle={() => setRailOpen(o => !o)}
        activeSessionID={sessionID}
        refreshKey={refreshKey}
        onSelect={selectSession}
        onNewChat={() => setNewChatRequest(request => request + 1)}
      />
      <div className="chat-column">
        <AssistantPanel
          sessionID={sessionID}
          onSessionChange={setSessionID}
          onSessionsChanged={bumpSessions}
          newChatRequest={newChatRequest}
        />
      </div>
    </main>
  )
}
