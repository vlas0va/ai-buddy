import { useId, useState } from 'react'
import type { UseQueryResult } from '@tanstack/react-query'
import type { z } from 'zod'
import type { actionSchema } from '@/api/schemas'
import { QueryState } from '@/components/QueryState'
import { Badge } from '@/components/ui/Badge'
import { Card } from '@/components/ui/Card'
import { ACTION_STATUS_TEXT, ACTION_STATUS_VARIANT, absenceSteps, withoutApprover } from '../statusText'
import { Steps } from './Steps'

type Action = z.infer<typeof actionSchema>

const KIND_TEXT: Record<Action['kind'], string> = {
  absence_request: 'Отсутствие',
  access_request: 'Доступ',
  trip_request: 'Командировка',
  consent: 'Согласие',
}

/** 2026-11-14T18:22:01Z → «14.11.2026, 21:22» (по часам браузера). */
function when(iso?: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return d.toLocaleString('ru-RU', { day: '2-digit', month: '2-digit', year: 'numeric', hour: '2-digit', minute: '2-digit' })
}

/**
 * Карточка заявки раскрывается по клику (Рита 15.11): согласующий, даты, шаги и комментарий к решению — например,
 * причина отказа, которую руководитель написал в «Согласованиях».
 */
function RequestCard({ a }: { a: Action }) {
  const [open, setOpen] = useState(false)
  const detailsId = useId()
  const rejected = a.status === 'rejected'
  const comment = a.decision_comment?.trim()
  const created = when(a.created_at)
  const updated = a.updated_at && a.updated_at !== a.created_at ? when(a.updated_at) : ''
  return (
    <Card className={`request-card${open ? ' is-open' : ''}`}>
      <button type="button" className="request-toggle" aria-expanded={open} aria-controls={detailsId} onClick={() => setOpen((v) => !v)}>
        <span className="request-head">
          <strong>{a.external_id ?? KIND_TEXT[a.kind]}</strong>
          <Badge variant={ACTION_STATUS_VARIANT[a.status] ?? 'neutral'}>{ACTION_STATUS_TEXT[a.status] ?? a.status}</Badge>
        </span>
        <span className="hint">{withoutApprover(a.summary)}</span>
        {!open && comment && <span className="request-more">{rejected ? 'Есть причина отказа — открыть' : 'Есть комментарий — открыть'}</span>}
      </button>
      {a.kind === 'absence_request' && a.status !== 'pending_approval' && <Steps steps={absenceSteps(a.status)} compact />}
      {open && (
        <div id={detailsId} className="request-details">
          {comment && (
            <div className={`request-comment${rejected ? ' is-rejected' : ''}`} role="note" aria-label={rejected ? 'Причина отказа' : 'Комментарий согласующего'}>
              <span className="request-comment-title">
                {rejected ? 'Причина отказа' : 'Комментарий'}
                {a.decided_by ? ` · ${a.decided_by}` : ''}
              </span>
              <p>{comment}</p>
            </div>
          )}
          <dl>
            <dt>Вид</dt>
            <dd>{KIND_TEXT[a.kind]}</dd>
            {a.approver_display_name && (
              <>
                <dt>Согласующий</dt>
                <dd>{a.approver_display_name}</dd>
              </>
            )}
            {created && (
              <>
                <dt>Создана</dt>
                <dd>{created}</dd>
              </>
            )}
            {updated && (
              <>
                <dt>Изменена</dt>
                <dd>{updated}</dd>
              </>
            )}
          </dl>
          {rejected && <p className="hint">Заявка убрана из плана. Чтобы подать новую, напиши buddy, например «хочу отгул на другую дату».</p>}
        </div>
      )}
    </Card>
  )
}

// «Мои заявки»: компактные карточки, общие для правой панели и выдвижной панели; по клику — подробности.
export function ActionsPanel({ query }: { query: Pick<UseQueryResult<Action[]>, 'data' | 'isPending' | 'error' | 'refetch'> }) {
  return (
    <QueryState
      isLoading={query.isPending}
      error={query.error}
      isEmpty={query.data?.length === 0}
      emptyText="Заявок пока нет."
      onRetry={() => void query.refetch()}
    >
      <ul className="list">
        {query.data?.map((a) => (
          <li key={a.id}>
            <RequestCard a={a} />
          </li>
        ))}
      </ul>
    </QueryState>
  )
}
