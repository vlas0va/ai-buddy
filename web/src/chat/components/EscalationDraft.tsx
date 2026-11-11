import { useMutation } from '@tanstack/react-query'
import { useState } from 'react'
import { sendEscalation } from '@/api/chat'
import { errorMessage } from '@/api/client'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import type { EscalationDraftData } from '@/chat/events'
import { ROLE_TEXT } from '../statusText'

/** `canSend`: core accepts escalations from interns only; other roles get the text to copy into their messenger. */
export function EscalationDraft({
  draft,
  sessionId,
  canSend = true,
  sent = false,
}: {
  draft: EscalationDraftData
  sessionId?: string
  canSend?: boolean
  /** already sent before (restored from the history): no second «Отправить» */
  sent?: boolean
}) {
  const [text, setText] = useState(draft.text)
  const mutation = useMutation({ mutationFn: () => sendEscalation({ to_role: draft.to_role, text, session_id: sessionId }) })
  const send = { ...mutation, isSuccess: mutation.isSuccess || sent }
  return (
    <Card className="chat-card" aria-label="Черновик сообщения">
      <h3>Черновик сообщения {ROLE_TEXT[draft.to_role]}</h3>
      <textarea
        className="ui-input chat-textarea"
        value={text}
        onChange={(e) => setText(e.target.value)}
        aria-label="Текст сообщения"
        rows={4}
        disabled={send.isSuccess}
      />
      {!canSend ? (
        <p className="hint">Скопируйте текст и отправьте в рабочий мессенджер.</p>
      ) : send.isSuccess ? (
        <p className="hint">Сообщение отправлено.</p>
      ) : (
        <div className="chat-actions">
          <Button loading={send.isPending} disabled={!text.trim()} onClick={() => send.mutate()}>
            Отправить наставнику
          </Button>
        </div>
      )}
      {send.isError && <p className="ui-field-error">{errorMessage(send.error)}</p>}
    </Card>
  )
}
