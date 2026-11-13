import { Link } from '@tanstack/react-router'
import type { ReactNode } from 'react'
import { RichText } from '@/components/rich/RichText'
import { ClipIcon } from '@/components/ui/icons'
import { Logo } from '@/components/ui/Logo'
import { KIND_TEXT, formatSize } from '../attachments'
import type { ChatMessage } from '../stream'
import { SourceChips } from './SourceChips'

export const PLAN_LINK_TEXT = 'Открыть мой план'

interface BubbleProps {
  message: ChatMessage
  typing: boolean
  status: string | null
  /** показывается в колонке buddy (рядом с аватаром), если у сообщения нет своего текста */
  error?: ReactNode
}

export function MessageBubble({ message, typing, status, error }: BubbleProps) {
  if (message.author === 'user') {
    const files = message.attachments ?? []
    return (
      <div className="chat-row chat-row-user">
        <div className="chat-bubble chat-bubble-user">
          {message.text}
          {files.length > 0 && (
            <div className="chat-attachments" aria-label="Вложения">
              {files.map((f, i) => (
                <span key={`${f.name}-${i}`} className="chat-attachment">
                  <ClipIcon /> {f.name} · {KIND_TEXT[f.kind]}, {formatSize(f.size)}
                </span>
              ))}
            </div>
          )}
        </div>
      </div>
    )
  }
  // «Открыть мой план» — ссылка на стороне клиента: в agent-stream.md нет события-ссылки, сценарий заканчивает текст
  // этой фразой.
  const planLink = message.text.trimEnd().endsWith(PLAN_LINK_TEXT)
  const body = planLink ? message.text.trimEnd().slice(0, -PLAN_LINK_TEXT.length).trimEnd() : message.text
  const showBubble = body !== '' || typing
  // строка статуса идёт перед ответом: она исчезает с первым токеном и никогда не остаётся под готовым ответом
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

/** Приветствие на стороне клиента для пустого разговора; на сервер никогда не отправляется. */
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
