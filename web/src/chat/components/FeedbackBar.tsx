import { useMutation } from '@tanstack/react-query'
import { useState } from 'react'
import { sendFeedback } from '@/api/feedback'

/** «Полезно / Не полезно» under a saved assistant answer (R24). One vote per message; a failed send can be repeated. */
export function FeedbackBar({ messageId }: { messageId: string }) {
  const [vote, setVote] = useState<boolean | null>(null)
  const send = useMutation({
    mutationFn: (useful: boolean) => sendFeedback(messageId, useful),
    onMutate: (useful) => setVote(useful),
    onError: () => setVote(null),
  })
  const sent = send.isSuccess
  return (
    <div className="chat-feedback" role="group" aria-label="Оценка ответа">
      {sent ? <span>Спасибо за оценку</span> : <span>Ответ помог?</span>}
      <button type="button" aria-pressed={vote === true} disabled={sent || send.isPending} onClick={() => send.mutate(true)}>
        Полезно
      </button>
      <button type="button" aria-pressed={vote === false} disabled={sent || send.isPending} onClick={() => send.mutate(false)}>
        Не полезно
      </button>
      {send.isError && <span className="ui-field-error">Не удалось отправить</span>}
    </div>
  )
}
