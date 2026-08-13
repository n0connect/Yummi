import { useState, useEffect, useCallback } from 'react'
import { AssistantPanel } from '../../features/assistant/AssistantPanel'
import { SessionRail } from '../../features/sessions/SessionRail'

const STORAGE_SID = 'yummi:chat:sessionId'

/**
 * Chat page shell. Owns the active session id so the collapsible session rail
 * and the assistant panel stay in sync: the rail switches sessions, the panel
 * hydrates and persists them. "default" is the legacy ad-hoc bucket.
 */
export function ChatPage(): JSX.Element {
  const [sessionID, setSessionID] = useState<string>(
    () => sessionStorage.getItem(STORAGE_SID) ?? 'default',
  )
  const [railOpen, setRailOpen] = useState(false)
  const [newChatRequest, setNewChatRequest] = useState(0)
  // Bumped whenever a session is created/renamed so the rail refetches its list.
  const [refreshKey, setRefreshKey] = useState(0)

  useEffect(() => {
    sessionStorage.setItem(STORAGE_SID, sessionID)
  }, [sessionID])

  const bumpSessions = useCallback(() => setRefreshKey(k => k + 1), [])

  const selectSession = useCallback((id: string) => {
    setSessionID(id)
    setRailOpen(false)
  }, [])

  return (
    <div className="chat-page">
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
    </div>
  )
}
