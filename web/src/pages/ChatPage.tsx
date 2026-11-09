import { useQuery } from '@tanstack/react-query'
import { listActions } from '@/api/actions'
import { keys } from '@/api/keys'
import { PageHeader } from '@/components/PageHeader'
import { QueryState } from '@/components/QueryState'
import { Badge } from '@/components/ui/Badge'
import { Card } from '@/components/ui/Card'

// Empty shell. The SSE conversation (src/chat/stream.ts) is added in a separate change.
export function ChatPage() {
  const actions = useQuery({ queryKey: keys.actions, queryFn: listActions })

  return (
    <section>
      <PageHeader title="Чат с buddy" hint="Диалог появится здесь. Ниже — ваши заявки, они сохраняются после перезагрузки." />
      <div className="page-section">
        <h2>Мои заявки</h2>
        <QueryState
          isLoading={actions.isPending}
          error={actions.error}
          isEmpty={actions.data?.length === 0}
          emptyText="Заявок пока нет."
          onRetry={() => void actions.refetch()}
        >
          <ul className="list">
            {actions.data?.map((a) => (
              <li key={a.id}>
                <Card className="list-row">
                  <span>
                    {a.external_id ?? a.id}: {a.summary}
                  </span>
                  <Badge>{a.status}</Badge>
                </Card>
              </li>
            ))}
          </ul>
        </QueryState>
      </div>
    </section>
  )
}
