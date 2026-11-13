import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import type { Proposal } from '@/chat/events'
import type { ProposalState } from '../stream'

const TYPE_TEXT: Record<string, string> = { day_off: 'Отгул', vacation: 'Отпуск', absence: 'Отсутствие', parental_leave: 'Декрет' }

const LEVEL_TEXT: Record<string, string> = { read: 'чтение', write: 'редактирование', admin: 'администрирование' }

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
      { label: 'Вид', value: TYPE_TEXT[str(args.type)] ?? str(args.type) },
      typeof display?.calendar_days === 'number'
        ? { label: 'Календарных дней', value: String(display.calendar_days) }
        : { label: 'Рабочих дней', value: typeof display?.working_days === 'number' ? String(display.working_days) : '' },
      { label: 'Документы', value: Array.isArray(display?.documents) ? display.documents.map(str).join(', ') : '' },
      { label: 'Согласующий', value: str(display?.approver_display_name) },
      { label: 'Затем', value: 'HR-администратор' },
      { label: 'Заменяет', value: str(display?.replaces) }, // отзыв 12.13: несогласованная заявка внутри новых дат
    ]
  }
  if (p.kind === 'absence_cancel') {
    return [
      { label: 'Заявка', value: `${str(display?.ref_id) || str(args.id)} — ${str(display?.type_label)}` },
      { label: 'Даты', value: str(display?.period_label) },
      { label: 'Что будет', value: 'заявка и её согласование отменятся, задача уйдёт из плана' },
    ]
  }
  if (p.kind === 'absence_reschedule') {
    return [
      { label: 'Заявка', value: `${str(display?.ref_id) || str(args.id)} — ${str(display?.type_label)}` },
      { label: 'Было', value: str(display?.old_period_label) },
      { label: 'Станет', value: str(display?.period_label) || `${str(args.start_date)} — ${str(args.end_date)}` },
      { label: 'Дальше', value: 'снова согласуют руководитель и HR-администратор' },
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
  // чекпоинт 11.11: конкретный ресурс, окружение, права и кто согласует
  return [
    { label: 'Ресурс', value: str(display?.resource_name) || str(args.resource_id) },
    { label: 'Окружение', value: str(args.environment) },
    { label: 'Права', value: str(display?.level_label) || (args.level ? LEVEL_TEXT[str(args.level)] ?? str(args.level) : '') },
    { label: 'Повышенные права', value: display?.elevated ? 'Да, нужно согласование' : 'Нет' },
    { label: 'Кто согласует', value: display?.elevated ? str(display?.approver_label) : '' },
    { label: 'Обоснование', value: str(display?.reason) || str(args.reason) },
  ]
}

const TITLE: Record<Proposal['kind'], string> = {
  absence_request: 'Черновик заявки на отсутствие',
  absence_reschedule: 'Перенос заявки',
  absence_cancel: 'Отмена заявки',
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
  const withdraw = proposal.kind === 'absence_cancel'
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
      {/* Рита 13.11: «Отменить» на карточке отмены читалось как «отменить заявку» — подписи называют итог */}
      {state === 'cancelled' && (
        <p className="hint">{withdraw ? 'Заявка осталась как была — отмена не отправлена.' : 'Черновик закрыт, ничего не отправлено.'}</p>
      )}
      {state === 'confirmed' && <p className="hint">{withdraw ? 'Отмена отправлена.' : 'Заявка отправлена.'}</p>}
      {!finished && (
        <div className="chat-actions">
          <Button loading={confirming} disabled={disabled && !confirming} onClick={() => onConfirm(proposal.proposal_id)}>
            {withdraw ? 'Отменить заявку' : 'Отправить'}
          </Button>
          <Button variant="secondary" disabled={confirming || disabled} onClick={() => onCancel(proposal.proposal_id)}>
            {withdraw ? 'Оставить заявку' : 'Не отправлять'}
          </Button>
        </div>
      )}
    </Card>
  )
}
