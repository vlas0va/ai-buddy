import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { setupServer } from 'msw/node'
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from 'vitest'
import { handlers } from '@/mocks/handlers'
import { resetMockState } from '@/mocks/db'
import { mockConfig } from '@/mocks/sse'
import { createAppRouter } from '@/router'
import { useChatStore } from '@/store/chat'
import { signInAs } from '@/test/mockWorld'

// S3 end to end on the mock server: intern creates -> lead approves -> intern sees "Согласовано".
const server = setupServer(...handlers)
beforeAll(() => server.listen())
afterAll(() => server.close())
afterEach(() => server.resetHandlers())

beforeEach(() => {
  resetMockState()
  useChatStore.setState({ sessions: {} })
  mockConfig.stepDelayMs = 0
})

function mountApp(path: string) {
  const router = createAppRouter(createMemoryHistory({ initialEntries: [path] }))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const view = render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  return { router, ...view }
}

describe('S3 step 8: manager approval', () => {
  it('S3_R66: intern creates, lead approves, intern sees "Согласовано" on the card and in "Мои заявки" after reload', async () => {
    signInAs('intern.anna')
    const intern = mountApp('/chat')
    await userEvent.type(await screen.findByLabelText('Сообщение'), 'хочу отгул в пятницу{Enter}')
    const draft = await screen.findByLabelText('Черновик заявки')
    await userEvent.click(within(draft).getByRole('button', { name: 'Отправить' }))
    expect(within(await screen.findByLabelText('Результат заявки')).getByText('На согласовании')).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Согласования' })).not.toBeInTheDocument()
    intern.unmount()

    signInAs('lead.maria')
    const lead = mountApp('/chat')
    await userEvent.click(await screen.findByRole('link', { name: 'Согласования' }))
    const card = await screen.findByLabelText('Заявка: Анна Соколова')
    await userEvent.click(within(card).getByRole('button', { name: 'Согласовать' }))
    expect(await screen.findByText('Заявок на согласование нет.')).toBeInTheDocument()
    lead.unmount()

    signInAs('intern.anna')
    const waiting = mountApp('/chat')
    expect(await within(await screen.findByLabelText('Результат заявки')).findByText('Ждёт HR-администратора')).toBeInTheDocument()
    waiting.unmount()

    signInAs('hradmin.olga')
    const admin = mountApp('/chat')
    expect(screen.queryByRole('link', { name: 'Мой план' })).not.toBeInTheDocument()
    await userEvent.click(await screen.findByRole('link', { name: 'Согласования' }))
    await userEvent.click(within(await screen.findByLabelText('Заявка: Анна Соколова')).getByRole('button', { name: 'Согласовать' }))
    expect(await screen.findByText('Заявок на согласование нет.')).toBeInTheDocument()
    admin.unmount()

    signInAs('intern.anna')
    mountApp('/chat')
    const result = await screen.findByLabelText('Результат заявки')
    expect(await within(result).findByText('Согласовано')).toBeInTheDocument()
    expect((await screen.findAllByText('Согласовано')).length).toBeGreaterThan(1) // card + "Мои заявки"
  })

  it('S3: a rejected request shows "Отклонено" to the intern', async () => {
    signInAs('intern.anna')
    const intern = mountApp('/chat')
    await userEvent.type(await screen.findByLabelText('Сообщение'), 'хочу отгул в пятницу{Enter}')
    await userEvent.click(within(await screen.findByLabelText('Черновик заявки')).getByRole('button', { name: 'Отправить' }))
    await screen.findByLabelText('Результат заявки')
    intern.unmount()

    signInAs('lead.maria')
    const lead = mountApp('/approvals')
    await userEvent.click(await screen.findByRole('button', { name: 'Отклонить' }))
    await screen.findByText('Заявок на согласование нет.')
    lead.unmount()

    signInAs('intern.anna')
    mountApp('/chat')
    expect(await within(await screen.findByLabelText('Результат заявки')).findByText('Отклонено')).toBeInTheDocument()
  })

  it('S3: an intern cannot open the approvals page and has no sidebar link', async () => {
    signInAs('intern.anna')
    const { router } = mountApp('/approvals')
    expect(await screen.findByRole('heading', { name: 'Чат с buddy' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/chat')
    expect(screen.queryByRole('link', { name: 'Согласования' })).not.toBeInTheDocument()
  })

  it('S3: a lead opens the approvals page directly', async () => {
    signInAs('lead.maria')
    const { router } = mountApp('/approvals')
    expect(await screen.findByRole('heading', { name: 'Согласования' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/approvals')
  })
})
