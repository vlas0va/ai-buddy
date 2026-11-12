import { QueryClient } from '@tanstack/react-query'
import { isRetryable } from '@/api/client'
import { useSession } from '@/store/session'

/**
 * QA 12.11: the cache is per user. Logging out and in as someone else in the same tab showed the previous user's
 * notifications and requests until a reload; any change of the signed-in user now drops every cached response.
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
