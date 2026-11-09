import { useQuery } from '@tanstack/react-query'
import { listActions } from '@/api/actions'
import { keys } from '@/api/keys'
import { QueryState } from '@/components/QueryState'

// Empty shell. The SSE conversation (src/chat/stream.ts) is added in a separate change.
export function ChatPage() {
  const actions = useQuery({ queryKey: keys.actions, queryFn: listActions })

  return (
    <section>
      <h1>Чат с buddy</h1>
      <p className="hint">Диалог появится здесь. Ниже — ваши заявки, они сохраняются после перезагрузки.</p>
      <h2>Мои заявки</h2>
      <QueryState
        isLoading={actions.isPending}
        error={actions.error}
        isEmpty={actions.data?.length === 0}
        emptyText="Заявок пока нет."
        onRetry={() => void actions.refetch()}
      >
        <ul>
          {actions.data?.map((a) => (
            <li key={a.id}>
              {a.external_id ?? a.id}: {a.summary} ({a.status})
            </li>
          ))}
        </ul>
      </QueryState>
    </section>
  )
}
