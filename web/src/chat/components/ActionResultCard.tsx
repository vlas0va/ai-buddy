import { Badge } from '@/components/ui/Badge'
import { Card } from '@/components/ui/Card'
import type { ActionResult } from '@/chat/events'
import { ACTION_STATUS_TEXT, ACTION_STATUS_VARIANT } from '../statusText'

export function ActionResultCard({ result }: { result: ActionResult }) {
  return (
    <Card className="chat-card chat-result" aria-label="Результат заявки">
      <h3>
        Заявка {result.external_id ?? result.action_id}{' '}
        <Badge variant={ACTION_STATUS_VARIANT[result.status] ?? 'neutral'}>{ACTION_STATUS_TEXT[result.status] ?? result.status}</Badge>
      </h3>
      {result.summary && <p>{result.summary}</p>}
      {result.approver_display_name && <p className="hint">Согласующий: {result.approver_display_name}</p>}
      <p className="hint">Сохранено в журнале действий</p>
    </Card>
  )
}
