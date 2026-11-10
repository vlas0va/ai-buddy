import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/client'
import { QueryState } from './QueryState'

describe('QueryState four states', () => {
  it('loading shows a skeleton', () => {
    render(<QueryState isLoading error={null}>ok</QueryState>)
    expect(screen.getByRole('status')).toBeInTheDocument()
  })

  it('error shows the message and retries', async () => {
    const onRetry = vi.fn()
    const error = new ApiError(503, { code: 'upstream_unavailable', message: 'Сервис недоступен', retryable: true })
    render(<QueryState isLoading={false} error={error} onRetry={onRetry}>ok</QueryState>)
    expect(screen.getByRole('alert')).toHaveTextContent('Сервис недоступен')
    await userEvent.click(screen.getByRole('button', { name: 'Повторить' }))
    expect(onRetry).toHaveBeenCalledOnce()
  })

  it('non-retryable error has no retry button', () => {
    const error = new ApiError(403, { code: 'forbidden', message: 'Нет доступа', retryable: false })
    render(<QueryState isLoading={false} error={error} onRetry={() => {}}>ok</QueryState>)
    expect(screen.queryByRole('button')).toBeNull()
  })

  it('empty and success', () => {
    const { rerender } = render(<QueryState isLoading={false} error={null} isEmpty emptyText="Пусто">ok</QueryState>)
    expect(screen.getByText('Пусто')).toBeInTheDocument()
    rerender(<QueryState isLoading={false} error={null} isEmpty>ok</QueryState>)
    expect(screen.getByText('Пока ничего нет.')).toBeInTheDocument()
    rerender(<QueryState isLoading={false} error={null}>ok</QueryState>)
    expect(screen.getByText('ok')).toBeInTheDocument()
  })
})
