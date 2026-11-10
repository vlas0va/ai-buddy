import type { UseQueryResult } from '@tanstack/react-query'
import type { z } from 'zod'
import type { actionSchema } from '@/api/schemas'
import { QueryState } from '@/components/QueryState'
import { Badge } from '@/components/ui/Badge'
import { Card } from '@/components/ui/Card'
import { ACTION_STATUS_TEXT, ACTION_STATUS_VARIANT, withoutApprover } from '../statusText'

type Action = z.infer<typeof actionSchema>

// "Мои заявки": compact cards, shared by the right-side panel and the drawer.
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
            <Card className="request-card">
              <div className="request-head">
                <strong>{a.external_id ?? a.id}</strong>
                <Badge variant={ACTION_STATUS_VARIANT[a.status] ?? 'neutral'}>{ACTION_STATUS_TEXT[a.status] ?? a.status}</Badge>
              </div>
              <p className="hint">{withoutApprover(a.summary)}</p>
            </Card>
          </li>
        ))}
      </ul>
    </QueryState>
  )
}
