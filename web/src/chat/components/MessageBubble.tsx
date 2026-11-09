import { Logo } from '@/components/ui/Logo'
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
      <Logo size={28} />
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

/** Client-side greeting for an empty conversation; never sent to the server. */
export function WelcomeBubble({ name }: { name: string }) {
  return (
    <div className="chat-row chat-row-buddy">
      <Logo size={28} />
      <div className="chat-col">
        <div className="chat-bubble chat-bubble-buddy">
          Привет, {name}! Я помогу с адаптацией: подскажу, кто есть кто, помогу оформить отгул и доступы. С чего начнём?
        </div>
      </div>
    </div>
  )
}
