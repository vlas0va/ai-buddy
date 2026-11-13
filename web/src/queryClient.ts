import { QueryClient } from '@tanstack/react-query'
import { isRetryable } from '@/api/client'
import { useSession } from '@/store/session'

/**
 * QA 12.11: кеш свой у каждого пользователя. Выход и вход под другим пользователем в той же вкладке показывал
 * уведомления и заявки предыдущего пользователя до перезагрузки; теперь любая смена вошедшего пользователя сбрасывает
 * все закешированные ответы.
 */
export function clearOnUserChange(client: QueryClient): () => void {
  let current = useSession.getState().user?.id ?? null
  return useSession.subscribe((s) => {
    const next = s.user?.id ?? null
    if (next !== current) {
      current = next
      client.clear()
    }
  })
}

export function createQueryClient() {
  const client = new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: 10_000,
        retry: (count, error) => isRetryable(error) && count < 1,
        refetchOnWindowFocus: false,
      },
    },
  })
  clearOnUserChange(client)
  return client
}
