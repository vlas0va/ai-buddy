import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useRef, useState } from 'react'
import { decideApproval, listApprovals, type Decision } from '@/api/approvals'
import { errorMessage } from '@/api/client'
import { keys } from '@/api/keys'
import type { z } from 'zod'
import type { approvalSchema } from '@/api/schemas'
import { withoutApprover } from '@/chat/statusText'
import { PageHeader } from '@/components/PageHeader'
import { QueryState } from '@/components/QueryState'
import { Badge } from '@/components/ui/Badge'
import { Button } from '@/components/ui/Button'
import { Card } from '@/components/ui/Card'
import './approvals.css'

type Approval = z.infer<typeof approvalSchema>

const KIND_TEXT: Record<Approval['kind'], string> = {
  absence_request: 'Отсутствие',
  access_request: 'Доступ',
  trip_request: 'Командировка',
}

function ApprovalCard({ approval }: { approval: Approval }) {
  const client = useQueryClient()
  const [comment, setComment] = useState('')
  // one key per card: a double click or a retry replays the same decision instead of creating a second one
  const idempotencyKey = useRef(crypto.randomUUID())
  const decide = useMutation({
    mutationFn: (decision: Decision) => decideApproval(approval.id, decision, comment.trim() || undefined, idempotencyKey.current),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: keys.approvals })
      void client.invalidateQueries({ queryKey: keys.actions })
      void client.invalidateQueries({ queryKey: ['plan'] })
    },
  })
  const busy = decide.isPending || decide.isSuccess
  // state updates are async: a ref also stops a second click that lands before the buttons are re-rendered as disabled
  const inFlight = useRef(false)
  function submit(decision: Decision) {
    if (inFlight.current) return
    inFlight.current = true
    decide.mutate(decision, {
      onError: () => {
        inFlight.current = false
      },
    })
  }
  return (
    <Card className="approval-card" aria-label={`Заявка: ${approval.requester_display_name}`}>
      <div className="approval-head">
        <strong>{approval.requester_display_name}</strong>
        <Badge variant="waiting_approval">{KIND_TEXT[approval.kind]}</Badge>
      </div>
      <p>{withoutApprover(approval.summary)}</p>
      <label className="approval-comment">
        <span className="hint">Комментарий (необязательно)</span>
        <textarea
          className="ui-input"
          rows={2}
          maxLength={1000}
          value={comment}
          disabled={busy}
          onChange={(e) => setComment(e.target.value)}
          aria-label={`Комментарий для ${approval.requester_display_name}`}
        />
      </label>
      {decide.isError && <p className="ui-field-error">{errorMessage(decide.error)}</p>}
      <div className="chat-actions">
        <Button loading={decide.isPending && decide.variables === 'approved'} disabled={busy} onClick={() => submit('approved')}>
          Согласовать
        </Button>
        <Button variant="secondary" loading={decide.isPending && decide.variables === 'rejected'} disabled={busy} onClick={() => submit('rejected')}>
          Отклонить
        </Button>
      </div>
    </Card>
  )
}

export function ApprovalsPage() {
  const query = useQuery({ queryKey: keys.approvals, queryFn: listApprovals, refetchOnWindowFocus: true })
  return (
    <section>
      <PageHeader title="Согласования" hint="Заявки сотрудников, ожидающие вашего решения." />
      <QueryState
        isLoading={query.isPending}
        error={query.error}
        isEmpty={query.data?.length === 0}
        emptyText="Заявок на согласование нет."
        onRetry={() => void query.refetch()}
      >
        <ul className="list">
          {query.data?.map((a) => (
            <li key={a.id}>
              <ApprovalCard approval={a} />
            </li>
          ))}
        </ul>
      </QueryState>
    </section>
  )
}
