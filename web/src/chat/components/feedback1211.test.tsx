import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from '@tanstack/react-router'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'
import type { ReactNode } from 'react'
import { afterAll, afterEach, beforeAll, describe, expect, it, vi } from 'vitest'
import { handlers } from '@/mocks/handlers'
import { askLabel, askText } from '@/plan/format'
import { signInAs } from '@/test/mockWorld'
import { ApprovalsHint } from './ApprovalsHint'
import { DatePickerCard } from './DatePickerCard'
import { DialogsMenu } from './DialogsMenu'

const server = setupServer(...handlers)
beforeAll(() => server.listen())
afterEach(() => server.resetHandlers())
afterAll(() => server.close())

const wrap = (ui: ReactNode) => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>)
}

describe('Feedback 12.11 / 12.13: a pop-up calendar for absence dates', () => {
  const picker = { purpose: 'absence' as const, kind: 'day_off', label: 'Даты отгула', min: '2026-11-13', max: '2027-11-13' }

  it('first day, then last day in a small calendar; a range is sent as a message and the card locks', async () => {
    const onPick = vi.fn()
    wrap(<DatePickerCard picker={{ ...picker, busy: [{ start: '2026-11-20', end: '2026-11-20', label: 'Отгул ABS-000100' }] }} onPick={onPick} />)
    const send = screen.getByRole('button', { name: 'Выбрать даты' })
    expect(send).toBeDisabled()
    await userEvent.click(screen.getByRole('button', { name: /Выбери даты/ }))
    expect(screen.getByRole('button', { name: '12 ноября 2026' })).toBeDisabled() // раньше min
    expect(screen.getByRole('button', { name: '20 ноября 2026, занято: Отгул ABS-000100' })).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '16 ноября 2026' }))
    await userEvent.click(screen.getByRole('button', { name: '17 ноября 2026' }))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument() // закрывается, как только диапазон выбран
    await userEvent.click(send)
    expect(onPick).toHaveBeenCalledWith('с 16.11.2026 по 17.11.2026')
    expect(screen.getByText(/Даты отправлены/)).toBeInTheDocument()
  })

  it('a reschedule names the request; one day is enough; the month can be switched', async () => {
    const onPick = vi.fn()
    wrap(<DatePickerCard picker={{ ...picker, purpose: 'reschedule', ref_id: 'ABS-000100', label: 'Новые даты: отгул ABS-000100' }} onPick={onPick} />)
    await userEvent.click(screen.getByRole('button', { name: /Выбери даты/ }))
    expect(screen.getByRole('button', { name: 'Предыдущий месяц' })).toBeDisabled()
    await userEvent.click(screen.getByRole('button', { name: 'Следующий месяц' }))
    await userEvent.click(screen.getByRole('button', { name: '2 декабря 2026' }))
    await userEvent.keyboard('{Escape}')
    await userEvent.click(screen.getByRole('button', { name: 'Перенести на эти даты' }))
    expect(onPick).toHaveBeenCalledWith('перенеси заявку ABS-000100 на 02.12.2026')
  })
})

describe('Feedback 12.11: earlier dialogs stay reachable', () => {
  it('the list shows dialogs, marks the open one and opens another', async () => {
    signInAs('intern.anna')
    server.use(
      http.get('*/api/chat/sessions', () =>
        HttpResponse.json([
          { session_id: 's2', title: 'кто мой наставник?', created_at: '2026-11-12T10:00:00Z', updated_at: '2026-11-12T10:01:00Z' },
          { session_id: 's1', title: 'Как оформить отгул?', created_at: '2026-11-12T09:00:00Z', updated_at: '2026-11-12T09:05:00Z' },
        ]),
      ),
    )
    const onOpen = vi.fn()
    wrap(<DialogsMenu current="s2" onOpen={onOpen} />)
    await userEvent.click(screen.getByRole('button', { name: 'Мои диалоги' }))
    expect(await screen.findByText('открыт сейчас')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('menuitem', { name: /Как оформить отгул/ }))
    expect(onOpen).toHaveBeenCalledWith('s1')
  })
})

describe('Feedback 12.11: the lead sees team requests waiting in «Согласования»', () => {
  it('shows the number of pending requests and a link', async () => {
    signInAs('lead.maria')
    server.use(http.get('*/api/approvals', () => HttpResponse.json([])))
    const rootRoute = createRootRoute({ component: () => <ApprovalsHint /> })
    const router = createRouter({ routeTree: rootRoute, history: createMemoryHistory({ initialEntries: ['/'] }) })
    wrap(<RouterProvider router={router} />)
    expect(await screen.findByText('Заявок, ожидающих вашего решения, нет.')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Открыть «Согласования»' })).toBeInTheDocument()
  })
})

describe('Feedback 12.11: «Спросить buddy» on an action task asks about the request', () => {
  it('action tasks ask for the status, checklist tasks ask how to do them', () => {
    expect(askText({ source: 'action', title: 'Отгул 13.11.2026' })).toBe('Что с моей заявкой «Отгул 13.11.2026»?')
    expect(askText({ source: 'action', title: 'Доступ: Jira' })).toBe('Какие доступы мне нужны?')
    expect(askText({ source: 'checklist', title: 'Познакомься с командой' })).toBe('Как выполнить задачу «Познакомься с командой»?')
    // организаторы 12.11: задача чек-листа про доступ ведёт прямо к заявке
    expect(askText({ source: 'checklist', title: 'Запроси доступ', checklist_item_id: 'CL-05' })).toBe('Какие доступы мне нужны?')
    expect(askLabel({ source: 'checklist', checklist_item_id: 'CL-05' })).toBe('Подать заявку')
    expect(askLabel({ source: 'checklist', checklist_item_id: 'CL-03' })).toBe('Спросить buddy')
    expect(askLabel({ source: 'action' })).toBe('Что с заявкой?')
  })
})
