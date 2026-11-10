import { Link } from '@tanstack/react-router'
import type { ReactNode } from 'react'
import { RichText } from '@/components/rich/RichText'
import { Logo } from '@/components/ui/Logo'
import type { ChatMessage } from '../stream'
import { SourceChips } from './SourceChips'

export const PLAN_LINK_TEXT = 'Открыть мой план'

interface BubbleProps {
  message: ChatMessage
  typing: boolean
  status: string | null
  /** rendered inside the buddy column (next to the avatar) when the message has no text of its own */
  error?: ReactNode
}

export function MessageBubble({ message, typing, status, error }: BubbleProps) {
  if (message.author === 'user') {
    return (
      <div className="chat-row chat-row-user">
        <div className="chat-bubble chat-bubble-user">{message.text}</div>
      </div>
    )
  }
  // "Открыть мой план" is a client-side link: agent-stream.md has no link event, the script ends its text with the phrase.
  const planLink = message.text.trimEnd().endsWith(PLAN_LINK_TEXT)
  const body = planLink ? message.text.trimEnd().slice(0, -PLAN_LINK_TEXT.length).trimEnd() : message.text
  const showBubble = body !== '' || typing
  // the status line belongs before the answer: it goes away with the first token and never stays under a finished answer
  const showStatus = typing && !!status && message.text === ''
  return (
    <div className="chat-row chat-row-buddy">
      <Logo size={28} />
      <div className="chat-col">
        {showStatus && (
          <p className="chat-status" role="status">
            {status}
          </p>
        )}
        {showBubble && (
          <div className="chat-bubble chat-bubble-buddy">
            {typing ? body : <RichText text={body} />}
            {typing && (
              <span className="chat-typing" aria-label="Buddy печатает">
                <i />
                <i />
                <i />
              </span>
            )}
          </div>
        )}
        {planLink && (
          <Link to="/plan" className="chat-link">
            {PLAN_LINK_TEXT}
          </Link>
        )}
        {error}
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
