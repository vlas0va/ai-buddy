import { QueryClient } from '@tanstack/react-query'
import { isRetryable } from '@/api/client'

export function createQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: 10_000,
        retry: (count, error) => isRetryable(error) && count < 1,
        refetchOnWindowFocus: false,
      },
    },
  })
}
