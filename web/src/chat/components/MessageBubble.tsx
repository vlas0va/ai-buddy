import { Avatar } from '@/components/ui/Avatar'
import type { ChatMessage } from '../stream'
import { SourceChips } from './SourceChips'

export function MessageBubble({ message, typing, status }: { message: ChatMessage; typing: boolean; status: string | null }) {
  if (message.author === 'user') {
    return (
      <div className="chat-row chat-row-user">
        <div className="chat-bubble chat-bubble-user">{message.text}</div>
      </div>
    )
  }
  const showBubble = message.text !== '' || typing
  return (
    <div className="chat-row chat-row-buddy">
      <Avatar name="AI Buddy" size="sm" />
      <div className="chat-col">
        {showBubble && (
          <div className="chat-bubble chat-bubble-buddy">
            {message.text}
            {typing && (
              <span className="chat-typing" aria-label="Buddy печатает">
                <i />
                <i />
                <i />
              </span>
            )}
          </div>
        )}
        {typing && status && (
          <p className="chat-status" role="status">
            {status}
          </p>
        )}
        <SourceChips sources={message.sources} />
      </div>
    </div>
  )
}
