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
      { label: 'Рабочих дней', value: typeof display?.working_days === 'number' ? String(display.working_days) : '' },
      { label: 'Согласующий', value: str(display?.approver_display_name) },
    ]
  }
  if (p.kind === 'trip_request') {
    const start = str(args.start_date)
    const end = str(args.end_date)
    return [
      { label: 'Город', value: str(args.city) },
      { label: 'Даты', value: str(display?.dates) || (start === end || !end ? start : `${start} — ${end}`) },
      { label: 'Цель', value: str(args.purpose) },
      { label: 'Согласующий', value: str(display?.approver_display_name) },
    ]
  }
  if (p.kind === 'consent') {
    return [
      { label: 'Согласие', value: str(display?.consent_title) || str(args.consent_id) },
      { label: 'Что это', value: str(display?.description) },
      { label: 'Отозвать', value: 'через HR BP' },
    ]
  }
  if (p.kind === 'approval_decision') {
    return [
      { label: 'Заявка', value: `${str(display?.ref_id)} — ${str(display?.requester_display_name)}` },
      { label: 'Что', value: str(display?.request_summary) },
      { label: 'Решение', value: str(display?.decision_label) },
    ]
  }
  return [
    { label: 'Ресурс', value: str(display?.resource_name) || str(args.resource_id) },
    { label: 'Повышенные права', value: display?.elevated ? 'Да, нужно согласование' : 'Нет' },
  ]
}

const TITLE: Record<Proposal['kind'], string> = {
  absence_request: 'Черновик заявки на отгул',
  access_request: 'Черновик заявки на доступ',
  trip_request: 'Черновик заявки на командировку',
  consent: 'Подпись согласия',
  approval_decision: 'Решение по заявке',
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
      <h3>{TITLE[proposal.kind]}</h3>
      <dl className="chat-rows">
        {proposalRows(proposal).filter((r) => r.value).map((r) => (
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
