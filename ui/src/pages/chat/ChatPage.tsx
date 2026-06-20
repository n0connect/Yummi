import { AssistantPanel } from '../../features/assistant/AssistantPanel'

export function ChatPage(): JSX.Element {
  return (
    <div className="chat-page">
      <div className="chat-column">
        <AssistantPanel />
      </div>
    </div>
  )
}
