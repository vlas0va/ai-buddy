import { useState } from 'react'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import type { EscalationDraftData } from '@/chat/events'
import { ROLE_TEXT } from '../statusText'

/**
 * Organizers 13.11: buddy does not deliver messages to the mentor — the mentor rarely opens buddy. The intern copies the
 * text into a direct message or the team channel; an unanswered question is already in the mentor's list (`queue`).
 * `messageId`, `canSend` and `sent` are kept for the call sites; nothing is sent from here.
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
      const el = document.getElementById(box) as HTMLTextAreaElement | null // no clipboard API: select for Ctrl+C
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
