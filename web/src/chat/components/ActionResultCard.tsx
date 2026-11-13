import { useQuery } from '@tanstack/react-query'
import { listActions } from '@/api/actions'
import { keys } from '@/api/keys'
import { Badge } from '@/components/ui/Badge'
import { Card } from '@/components/ui/Card'
import type { ActionResult } from '@/chat/events'
import { ACTION_STATUS_TEXT, ACTION_STATUS_VARIANT, absenceSteps, withoutApprover } from '../statusText'
import { Steps } from './Steps'

export function ActionResultCard({ result }: { result: ActionResult }) {
  // карточка — снимок из потока; актуальный статус (approved/rejected) берётся из списка действий (перезапрашивается
  // при фокусе)
  const actions = useQuery({ queryKey: keys.actions, queryFn: listActions, refetchOnWindowFocus: true })
  const live = actions.data?.find((a) => a.id === result.action_id)
  const status = live?.status ?? result.status
  return (
    <Card className="chat-card chat-result" aria-label="Результат заявки">
      <h3>
        Заявка {result.external_id ?? result.action_id}{' '}
        <Badge variant={ACTION_STATUS_VARIANT[status] ?? 'neutral'}>{ACTION_STATUS_TEXT[status] ?? status}</Badge>
      </h3>
      {result.summary && <p>{withoutApprover(result.summary)}</p>}
      {result.approver_display_name && <p className="hint">Согласующий: {result.approver_display_name}</p>}
      {result.kind === 'absence_request' && <Steps steps={absenceSteps(status)} />}
      <p className="hint chat-saved">Сохранено в журнале действий</p>
    </Card>
  )
}
