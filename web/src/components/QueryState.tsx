import type { ReactNode } from 'react'
import { errorMessage, isRetryable } from '@/api/client'
import { Skeleton } from './Skeleton'

interface Props {
  isLoading: boolean
  error: unknown
  isEmpty?: boolean
  emptyText?: string
  onRetry?: () => void
  children: ReactNode
}

// The four states every view must design: loading, empty, error (with retry), success.
export function QueryState({ isLoading, error, isEmpty, emptyText, onRetry, children }: Props) {
  if (isLoading) return <Skeleton />
  if (error) {
    return (
      <div className="state state-error" role="alert">
        <p>{errorMessage(error)}</p>
        {onRetry && isRetryable(error) && (
          <button type="button" onClick={onRetry}>
            Повторить
          </button>
        )}
      </div>
    )
  }
  if (isEmpty) return <div className="state state-empty">{emptyText ?? 'Пока ничего нет.'}</div>
  return <>{children}</>
}
