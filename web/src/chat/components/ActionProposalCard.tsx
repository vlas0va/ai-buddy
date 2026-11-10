import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import type { Proposal } from '@/chat/events'
import type { ProposalState } from '../stream'

const TYPE_TEXT: Record<string, string> = { day_off: 'Отгул', vacation: 'Отпуск', sick: 'Больничный' }

function str(v: unknown): string {
  return typeof v === 'string' ? v : ''
}

function proposalRows(p: Proposal): { label: string; value: string }[] {
  const { args, display } = p.draft
  if (p.kind === 'absence_request') {
    const start = str(args.start_date)
    const end = str(args.end_date)
    return [
      { label: 'Даты', value: str(display?.dates) || (start === end || !end ? start : `${start} — ${end}`) },
      { label: 'Тип', value: TYPE_TEXT[str(args.type)] ?? str(args.type) },
      { label: 'Согласующий', value: str(display?.approver_display_name) },
    ]
  }
  return [
    { label: 'Ресурс', value: str(display?.resource_name) || str(args.resource_id) },
    { label: 'Повышенные права', value: display?.elevated ? 'Да, нужно согласование' : 'Нет' },
  ]
}

interface Props {
  proposal: Proposal & { state: ProposalState }
  disabled?: boolean
  onConfirm: (proposalId: string) => void
  onCancel: (proposalId: string) => void
}

export function ActionProposalCard({ proposal, disabled, onConfirm, onCancel }: Props) {
  const { state } = proposal
  const confirming = state === 'confirming'
  const finished = state === 'confirmed' || state === 'cancelled'
  return (
    <Card className="chat-card" aria-label="Черновик заявки">
      <h3>{proposal.kind === 'absence_request' ? 'Черновик заявки на отгул' : 'Черновик заявки на доступ'}</h3>
      <dl className="chat-rows">
        {proposalRows(proposal).map((r) => (
          <div key={r.label}>
            <dt>{r.label}</dt>
            <dd>{r.value}</dd>
          </div>
        ))}
      </dl>
      {state === 'cancelled' && <p className="hint">Заявка отменена, ничего не отправлено.</p>}
      {state === 'confirmed' && <p className="hint">Заявка отправлена.</p>}
      {!finished && (
        <div className="chat-actions">
          <Button loading={confirming} disabled={disabled && !confirming} onClick={() => onConfirm(proposal.proposal_id)}>
            Отправить
          </Button>
          <Button variant="secondary" disabled={confirming || disabled} onClick={() => onCancel(proposal.proposal_id)}>
            Отменить
          </Button>
        </div>
      )}
    </Card>
  )
}
