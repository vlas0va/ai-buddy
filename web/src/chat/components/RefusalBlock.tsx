import type { Refusal } from '@/chat/events'
import { ROLE_TEXT } from '../statusText'

export function RefusalBlock({ refusal }: { refusal: Refusal }) {
  const who = refusal.redirect_role ? ROLE_TEXT[refusal.redirect_role] : null
  const base =
    refusal.reason === 'no_rights'
      ? 'У вас нет доступа к этой информации или операции.'
      : 'Я не могу помочь с этим вопросом.'
  return (
    <div className="chat-refusal" role="note">
      {base} {who ? `Обратитесь к ${who}.` : ''}
    </div>
  )
}
