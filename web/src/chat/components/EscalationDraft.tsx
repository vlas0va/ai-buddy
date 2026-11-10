import { useMutation } from '@tanstack/react-query'
import { useState } from 'react'
import { sendEscalation } from '@/api/chat'
import { errorMessage } from '@/api/client'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import type { EscalationDraftData } from '@/chat/events'
import { ROLE_TEXT } from '../statusText'

export function EscalationDraft({ draft, sessionId }: { draft: EscalationDraftData; sessionId?: string }) {
  const [text, setText] = useState(draft.text)
  const send = useMutation({ mutationFn: () => sendEscalation({ to_role: draft.to_role, text, session_id: sessionId }) })
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
      {send.isSuccess ? (
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
