import type { ReactNode } from 'react'
import { errorMessage, isRetryable } from '@/api/client'
import { Skeleton } from './Skeleton'
import { EmptyState } from './ui/EmptyState'
import { ErrorState } from './ui/ErrorState'

interface Props {
  isLoading: boolean
  error: unknown
  isEmpty?: boolean
  emptyText?: string
  onRetry?: () => void
  children: ReactNode
}

// Четыре состояния, которые должен продумать каждый экран: загрузка, пусто, ошибка (с повтором), успех.
export function QueryState({ isLoading, error, isEmpty, emptyText, onRetry, children }: Props) {
  if (isLoading) return <Skeleton />
  if (error) {
    return <ErrorState message={errorMessage(error)} onRetry={onRetry && isRetryable(error) ? onRetry : undefined} />
  }
  if (isEmpty) return <EmptyState>{emptyText ?? 'Пока ничего нет.'}</EmptyState>
  return <>{children}</>
}
