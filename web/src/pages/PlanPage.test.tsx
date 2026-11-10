import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { jsonResponse } from '@/test/helpers'
import { ChatPage } from './ChatPage'
import { PlanPage } from './PlanPage'

function wrap(ui: React.ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>)
}

const plan = {
  user_id: 'u1',
  plan_role: 'intern',
  current_period: 'week_1',
  periods: [
    {
      id: 'week_1',
      label: '1 неделя',
      tasks: [{ id: 't1', source: 'checklist', checklist_item_id: 'CL-03', period: 'week_1', title: 'Познакомиться с командой', status: 'not_started', human_only: false }],
    },
  ],
  goals: [],
}

describe('PlanPage states', () => {
  it('loading then success', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(plan)))
    wrap(<PlanPage />)
    expect(screen.getByRole('status')).toBeInTheDocument()
    expect(await screen.findByText('Познакомиться с командой')).toBeInTheDocument()
  })

  it('empty', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ ...plan, periods: [] })))
    wrap(<PlanPage />)
    expect(await screen.findByText(/План пока пуст/)).toBeInTheDocument()
  })

  it('error with retry', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse({ code: 'upstream_unavailable', message: 'Сервис недоступен', retryable: true }, 503))
      .mockResolvedValueOnce(jsonResponse(plan))
    vi.stubGlobal('fetch', fetchMock)
    wrap(<PlanPage />)
    expect(await screen.findByRole('alert')).toHaveTextContent('Сервис недоступен')
    await userEvent.click(screen.getByRole('button', { name: 'Повторить' }))
    expect(await screen.findByText('Познакомиться с командой')).toBeInTheDocument()
  })
})

describe('ChatPage states', () => {
  it('shows the saved actions', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        jsonResponse([{ id: 'a1', kind: 'absence_request', external_id: 'ABS-000123', status: 'pending_approval', summary: 'Отгул 14.11.2026', created_at: '2026-11-09T10:00:00Z' }]),
      ),
    )
    wrap(<ChatPage />)
    expect(await screen.findByText(/ABS-000123/)).toBeInTheDocument()
  })

  it('empty and error', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValueOnce(jsonResponse([])))
    const first = wrap(<ChatPage />)
    expect(await screen.findByText('Заявок пока нет.')).toBeInTheDocument()
    first.unmount()

    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ code: 'internal', message: 'Ошибка сервера', retryable: true }, 500)))
    wrap(<ChatPage />)
    expect(await screen.findByRole('alert')).toHaveTextContent('Ошибка сервера')
  })
})
