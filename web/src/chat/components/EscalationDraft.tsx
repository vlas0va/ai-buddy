import { useState } from 'react'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import type { EscalationDraftData } from '@/chat/events'
import { ROLE_TEXT } from '../statusText'

/**
 * Организаторы 13.11: buddy не передаёт сообщения наставнику — наставник редко открывает buddy. Стажёр копирует текст в
 * личные сообщения или канал команды; вопрос без ответа уже в списке наставника (`queue`). `messageId`, `canSend` и
 * `sent` оставлены для мест вызова; отсюда ничего не отправляется.
 */
export function EscalationDraft({ draft }: { draft: EscalationDraftData; sessionId?: string; messageId?: string; canSend?: boolean; sent?: boolean }) {
  const [text, setText] = useState(draft.text)
  const [copied, setCopied] = useState(false)
  const box = `textarea-${draft.to_role}`
  async function copy() {
    try {
      await navigator.clipboard.writeText(text)
      setCopied(true)
    } catch {
      const el = document.getElementById(box) as HTMLTextAreaElement | null // нет API буфера обмена: выделяем текст для Ctrl+C
      el?.select()
    }
  }
  const who = draft.to_name ? ` (${draft.to_name})` : ''
  return (
    <Card className="chat-card" aria-label="Черновик сообщения">
      <h3>Сообщение {ROLE_TEXT[draft.to_role]}</h3>
      <textarea id={box} className="ui-input chat-textarea" value={text} onChange={(e) => setText(e.target.value)} aria-label="Текст сообщения" rows={4} />
      <div className="chat-actions">
        <Button variant="secondary" size="sm" disabled={!text.trim()} onClick={() => void copy()}>
          {copied ? 'Скопировано' : 'Скопировать'}
        </Button>
        <span className="hint">
          Отправь в личные сообщения{who}
          {draft.channel ? ` или в канал ${draft.channel}` : ''}.
        </span>
      </div>
      {draft.queue && (
        <p className="hint">Вопрос уже в списке {draft.to_role === 'lead' ? 'руководителя' : draft.to_role === 'hrbp' ? 'HR BP' : 'наставника'}: ответ придёт в «Мои вопросы» и попадёт в базу знаний.</p>
      )}
    </Card>
  )
}
