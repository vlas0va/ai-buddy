import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from '@tanstack/react-router'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactNode } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { parseBlocks } from '@/components/rich/blocks'
import { RichText } from '@/components/rich/RichText'
import { jsonResponse } from '@/test/helpers'
import { parseEvent } from '../events'
import { handlers, initialState, reduce, startTurn } from '../stream'
import { FeedbackBar } from './FeedbackBar'
import { NextStepCard } from './NextStepCard'

function withRouter(ui: ReactNode) {
  const router = createRouter({ routeTree: createRootRoute({ component: () => <>{ui}</> }), history: createMemoryHistory() })
  const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
}

describe('next_step and done in the reducer (S1, R24)', () => {
  it('stores the next step and the saved message id; unknown events change nothing', () => {
    let s = startTurn(initialState, 'что дальше?')
    s = reduce(s, parseEvent('next_step', JSON.stringify({ task_id: 't1', title: 'Получить доступы', cta: { kind: 'access', label: 'Запросить доступы', message: 'Какие доступы мне нужны?' } })))
    s = reduce(s, parseEvent('future_event', '{}'))
    s = reduce(s, { type: 'done', data: { message_id: '7f7c1c1e-0000-4000-8000-000000000001' } })
    const last = s.messages.at(-1)!
    expect(last.nextStep?.title).toBe('Получить доступы')
    expect(last.serverId).toBe('7f7c1c1e-0000-4000-8000-000000000001')
    expect(s.streaming).toBe(false)
    expect(handlers.done(s).streaming).toBe(false)
  })
})

describe('NextStepCard', () => {
  it('sends the suggested message', async () => {
    const onAsk = vi.fn()
    withRouter(
      <NextStepCard
        step={{ title: 'Познакомься с командой', human_only: true, cta: { kind: 'ask', label: 'Спросить buddy', message: 'Кто есть кто?' } }}
        disabled={false}
        onAsk={onAsk}
      />,
    )
    expect(await screen.findByText('Познакомься с командой')).toBeInTheDocument()
    expect(screen.getByText(/делаешь ты сам/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Спросить buddy' }))
    expect(onAsk).toHaveBeenCalledWith('Кто есть кто?')
    expect(screen.getByRole('link', { name: 'Открыть план' })).toBeInTheDocument()
  })
})

describe('FeedbackBar', () => {
  it('sends one vote and thanks', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)
    withRouter(<FeedbackBar messageId="m-1" />)
    await userEvent.click(await screen.findByRole('button', { name: 'Полезно' }))
    expect(await screen.findByText('Спасибо за оценку')).toBeInTheDocument()
    expect(JSON.parse(fetchMock.mock.calls[0]![1].body as string)).toEqual({ message_id: 'm-1', useful: true })
    expect(screen.getByRole('button', { name: 'Не полезно' })).toBeDisabled()
  })

  it('lets the user retry after a failure', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ code: 'internal', message: 'x', retryable: true }, 500)))
    withRouter(<FeedbackBar messageId="m-2" />)
    await userEvent.click(await screen.findByRole('button', { name: 'Не полезно' }))
    expect(await screen.findByText('Не удалось отправить')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Не полезно' })).not.toBeDisabled()
  })
})

describe('RichText', () => {
  it('renders lists, bold, code and tables without raw markdown', () => {
    const text = '## Шаги\nСделай так:\n1. **Склонируй** репозиторий\n2. Выполни `npm ci`\n\n- пункт\n- ещё\n\n| Бенефит | Сумма |\n|---|---|\n| Спорт | 3 000 ₽ |'
    const { container } = render(<RichText text={text} />)
    expect(container.querySelector('ol')?.children).toHaveLength(2)
    expect(container.querySelector('ul')?.children).toHaveLength(2)
    expect(screen.getByText('Склонируй').tagName).toBe('STRONG')
    expect(screen.getByText('npm ci').tagName).toBe('CODE')
    expect(screen.getByRole('table')).toHaveTextContent('Спорт')
    expect(container.textContent).not.toContain('##')
    expect(container.textContent).not.toContain('**')
  })

  it('never turns text into markup', () => {
    const { container } = render(<RichText text={'<img src=x onerror=alert(1)>\nстрока'} />)
    expect(container.querySelector('img')).toBeNull()
    expect(container.querySelector('br')).not.toBeNull()
    expect(parseBlocks('\n\n')).toEqual([])
    // Рита 12.13: нумерованные разделы, разбитые пунктами, сохраняют одну нумерацию, даже если модель каждый раз писала
    // «1.»
    const starts = (t: string) => parseBlocks(t).flatMap((x) => (x.kind === 'ol' ? [x.start] : []))
    expect(starts('1. **Кто**:\n- наставник\n\n1. **Правила**:\n- до 400 строк\n1. **Сроки**:\n- день')).toEqual([1, 2, 3])
    expect(starts('1. А\n- x\n2. Б\n- y\n3. В')).toEqual([1, 2, 3])
    expect(starts('1. А\n2. Б\n\nТекст между списками.\n\n1. Новый список')).toEqual([1, 1])
  })
})

describe('EscalationDraft for non-interns', () => {
  it('offers the text to copy instead of sending (organizers 13.11)', async () => {
    const { EscalationDraft } = await import('./EscalationDraft')
    withRouter(<EscalationDraft draft={{ to_role: 'lead', text: 'Привет' }} canSend={false} />)
    expect(await screen.findByRole('button', { name: 'Скопировать' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Отправить/ })).not.toBeInTheDocument()
  })
})
