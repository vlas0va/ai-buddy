import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from 'vitest'
import { resetMockState, state } from '@/mocks/db'
import { handlers } from '@/mocks/handlers'
import { mockConfig } from '@/mocks/sse'
import { createAppRouter } from '@/router'
import { useAsk } from '@/store/ask'
import { useChatStore } from '@/store/chat'
import { signInAs } from '@/test/mockWorld'

// S1 plan page, mentor / lead views, questions and notifications against the dev-mock handlers.
const server = setupServer(...handlers)
beforeAll(() => server.listen())
afterAll(() => server.close())
afterEach(() => server.resetHandlers())

beforeEach(() => {
  resetMockState()
  useChatStore.setState({ sessions: {} })
  useAsk.setState({ pending: null })
  mockConfig.stepDelayMs = 0
})

function mountAt(path: string) {
  const router = createAppRouter(createMemoryHistory({ initialEntries: [path] }))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  return router
}

describe('S1 plan page', () => {
  it('S1_R18: shows progress, timeline with the current period, due dates, subtasks and the next step', async () => {
    signInAs('intern.anna')
    mountAt('/plan')
    expect(await screen.findByText('Настроить рабочее окружение')).toBeInTheDocument()
    expect(screen.getByRole('progressbar', { name: 'Прогресс плана' })).toHaveAttribute('aria-valuenow', '1')
    expect(screen.getByRole('list', { name: 'Этапы стажировки' }).querySelector('[aria-current="step"]')).toHaveTextContent('2 неделя')
    expect(screen.getByText('до 13.11')).toBeInTheDocument()
    expect(screen.getByText('Jira')).toBeInTheDocument()
    const next = screen.getByLabelText('Следующий шаг')
    expect(within(next).getByText('Получить доступы')).toBeInTheDocument()
    expect(screen.getAllByText(/делаешь вместе с командой/).length).toBeGreaterThan(0)
  })

  it('S1_R76: marking a task done is optimistic and survives a reload', async () => {
    signInAs('intern.anna')
    mountAt('/plan')
    const box = await screen.findByLabelText('Отметить выполненной: Пройти вводный курс по безопасности')
    await userEvent.click(box)
    expect(box).toBeChecked()
    await waitFor(() => expect(state.taskStatus.get('intern.anna:00000000-0000-4000-8000-000000000005')).toBe('done'))
    expect(await screen.findByLabelText('Вернуть в работу: Пройти вводный курс по безопасности')).toBeChecked()
  })

  it('S1_R76: a failed update rolls the checkbox back with a message', async () => {
    signInAs('intern.anna')
    server.use(http.patch('*/api/plan/tasks/:id', () => HttpResponse.json({ code: 'conflict', message: 'Статус меняется автоматически.', retryable: false }, { status: 409 })))
    mountAt('/plan')
    const box = await screen.findByLabelText('Отметить выполненной: Пройти вводный курс по безопасности')
    await userEvent.click(box)
    expect(await screen.findByText(/Статус возвращён/)).toBeInTheDocument()
    expect(screen.getByLabelText('Отметить выполненной: Пройти вводный курс по безопасности')).not.toBeChecked()
  })

  it('S1: a subtask completes its parent', async () => {
    signInAs('intern.anna')
    mountAt('/plan')
    const jira = await screen.findByRole('checkbox', { name: 'Jira' })
    await userEvent.click(jira)
    expect(await screen.findByLabelText('Вернуть в работу: Получить доступы')).toBeChecked()
  })

  it('S1_R19: «Спросить buddy» opens the chat and sends the question', async () => {
    signInAs('intern.anna')
    const router = mountAt('/plan')
    const next = await screen.findByLabelText('Следующий шаг')
    await userEvent.click(within(next).getByRole('button', { name: 'Спросить buddy' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/chat'))
    expect(await screen.findByText('Как выполнить задачу «Получить доступы»?')).toBeInTheDocument()
    expect(useAsk.getState().pending).toBeNull()
  })

  it('S1: the intern sees goals read-only', async () => {
    state.goals.set('intern.anna', [{ id: 'g1', created_by: 'mentor.oleg', title: 'Сделать первую задачу', criteria: 'MR смержен' }])
    signInAs('intern.anna')
    mountAt('/plan')
    expect(await screen.findByText('Сделать первую задачу')).toBeInTheDocument()
    expect(screen.getByText('Критерий: MR смержен')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Изменить цели' })).not.toBeInTheDocument()
  })
})

describe('mentor and lead views', () => {
  it('S1_R08: the mentor sees team interns with progress and opens one plan read-only', async () => {
    signInAs('mentor.oleg')
    mountAt('/team')
    const row = await screen.findByRole('link', { name: 'План: Анна Соколова' })
    expect(row).toHaveTextContent('Готово 1 из 6')
    expect(screen.queryByText('Иван Петров')).not.toBeInTheDocument() // other team (R71)
    await userEvent.click(row)
    expect(await screen.findByText('План: Анна Соколова')).toBeInTheDocument()
    expect(await screen.findByLabelText('Отметить выполненной: Настроить рабочее окружение')).toBeDisabled()
    expect(screen.queryByLabelText('Следующий шаг')).not.toBeInTheDocument()
  })

  it('S1_R20: the mentor sets goals with criteria and the intern sees them', async () => {
    signInAs('mentor.oleg')
    mountAt('/team/intern.anna')
    await userEvent.click(await screen.findByRole('button', { name: 'Задать цели' }))
    await userEvent.type(screen.getByLabelText('Цель 1'), 'Выкатить первую задачу')
    await userEvent.type(screen.getByLabelText('Критерий 1'), 'Задача в проде')
    await userEvent.click(screen.getByRole('button', { name: 'Добавить цель' }))
    await userEvent.type(screen.getByLabelText('Цель 2'), 'Выступить на демо')
    await userEvent.click(screen.getAllByRole('button', { name: 'Удалить' })[0]!)
    await userEvent.click(screen.getByRole('button', { name: 'Сохранить' }))
    expect(await screen.findByText('Выступить на демо')).toBeInTheDocument()
    expect(state.goals.get('intern.anna')?.map((g) => g.title)).toEqual(['Выступить на демо'])
  })

  it('a mentor cannot open an intern of another team', async () => {
    signInAs('mentor.oleg')
    mountAt('/team/intern.ivan')
    expect(await screen.findByRole('alert')).toHaveTextContent('Недостаточно прав')
  })

  it('an intern is redirected away from /team', async () => {
    signInAs('intern.anna')
    const router = mountAt('/team')
    await waitFor(() => expect(router.state.location.pathname).toBe('/chat'))
    expect(screen.queryByRole('link', { name: 'Мои стажёры' })).not.toBeInTheDocument()
  })

  it('the lead sees the team page and approvals in the menu, not a personal plan', async () => {
    signInAs('lead.maria')
    mountAt('/team')
    expect(await screen.findByRole('link', { name: 'План: Анна Соколова' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Согласования' })).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Мой план' })).not.toBeInTheDocument()
  })
})

describe('questions and notifications', () => {
  it('S2_R10: intern sends a question, mentor answers and adds it to the KB, intern gets a notification', async () => {
    signInAs('intern.anna')
    state.escalations.push({ id: 'e1', from_id: 'intern.anna', from_display_name: 'Анна Соколова', to_role: 'mentor', text: 'Где лежит дизайн-ревью?', status: 'open', answer: null, added_to_kb: false, created_at: '2026-11-10T09:00:00Z' })
    mountAt('/questions')
    expect(await screen.findByText('Где лежит дизайн-ревью?')).toBeInTheDocument()
    expect(screen.getByText('Ждёт ответа')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Ответить' })).not.toBeInTheDocument()
  })

  it('S2_R10: mentor answers with add_to_kb', async () => {
    state.escalations.push({ id: 'e1', from_id: 'intern.anna', from_display_name: 'Анна Соколова', to_role: 'mentor', text: 'Где лежит дизайн-ревью?', status: 'open', answer: null, added_to_kb: false, created_at: '2026-11-10T09:00:00Z' })
    signInAs('mentor.oleg')
    mountAt('/questions')
    await userEvent.type(await screen.findByLabelText('Ответ для Анна Соколова'), 'В Confluence, раздел «Дизайн»')
    await userEvent.click(screen.getByLabelText(/Добавить ответ в базу знаний/))
    expect(screen.getByText(/без имён и конкретных дат/)).toBeInTheDocument() // checkpoint 11.11: the FAQ is general
    await userEvent.click(screen.getByRole('button', { name: 'Ответить' }))
    expect(await screen.findByText('Есть ответ')).toBeInTheDocument()
    expect(screen.getByText('Ответ добавлен в базу знаний команды.')).toBeInTheDocument()
    expect(state.notifications.get('intern.anna')?.[0]?.kind).toBe('escalation_answer')
  })

  it('empty questions list', async () => {
    signInAs('mentor.oleg')
    mountAt('/questions')
    expect(await screen.findByText('Новых вопросов нет.')).toBeInTheDocument()
  })

  it('R54: the bell shows unread reminders; opening the panel reads them (QA 12.11)', async () => {
    signInAs('intern.anna')
    mountAt('/plan')
    const bell = await screen.findByRole('button', { name: /Уведомления/ })
    expect(await within(bell).findByLabelText('Непрочитанных: 1')).toBeInTheDocument()
    await userEvent.click(bell)
    const panel = screen.getByRole('region', { name: 'Уведомления' })
    expect(within(panel).getByText('Напоминание: вторая неделя')).toBeInTheDocument()
    expect(within(panel).queryByRole('button', { name: 'Прочитано' })).not.toBeInTheDocument()
    await waitFor(() => expect(within(bell).queryByLabelText(/Непрочитанных/)).not.toBeInTheDocument())
    await userEvent.keyboard('{Escape}')
    expect(screen.queryByRole('region', { name: 'Уведомления' })).not.toBeInTheDocument()
  })

  it('the bell says when there is nothing and closes on an outside click', async () => {
    signInAs('hrbp.elena')
    mountAt('/questions')
    await userEvent.click(await screen.findByRole('button', { name: /Уведомления/ }))
    expect(await screen.findByText('Уведомлений нет.')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('heading', { level: 1 }))
    expect(screen.queryByText('Уведомлений нет.')).not.toBeInTheDocument()
  })
})
