import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { setupServer } from 'msw/node'
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import { handlers } from '@/mocks/handlers'
import { MOCK_USERS, resetMockState } from '@/mocks/db'
import { mockConfig } from '@/mocks/sse'
import { useSession } from '@/store/session'
import { ChatPage } from './ChatPage'

// Full S3 flow against the same MSW handlers that power `npm run dev:mock`.
const server = setupServer(...handlers)
beforeAll(() => server.listen())
afterAll(() => server.close())
afterEach(() => server.resetHandlers())

beforeEach(() => {
  resetMockState()
  mockConfig.stepDelayMs = 0
  const anna = MOCK_USERS[0]!
  useSession.setState({ token: `mock-token-${anna.login}`, user: anna })
})

function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const spy = vi.spyOn(client, 'invalidateQueries')
  const view = render(
    <QueryClientProvider client={client}>
      <ChatPage />
    </QueryClientProvider>,
  )
  return { client, spy, ...view }
}

async function ask(text: string) {
  await userEvent.type(screen.getByLabelText('Сообщение'), `${text}{Enter}`)
}

describe('S3 day off flow', () => {
  it('S3_R66: message -> draft -> confirm -> result card -> saved and visible after reload', async () => {
    const { spy, unmount } = mount()
    expect(screen.getAllByRole('button', { name: /отгул|доступы|наставник/i })).toHaveLength(3)
    expect(await screen.findByText('Заявок пока нет.')).toBeInTheDocument()

    await ask('хочу отгул в пятницу')
    const draft = await screen.findByLabelText('Черновик заявки')
    expect(within(draft).getByText('Мария Лебедева')).toBeInTheDocument()
    expect(screen.getByText('Тестовая инструкция прототипа')).toBeInTheDocument() // R69
    // nothing is created before the click
    expect(screen.queryByLabelText('Результат заявки')).not.toBeInTheDocument()

    await userEvent.click(within(draft).getByRole('button', { name: 'Отправить' }))
    const result = await screen.findByLabelText('Результат заявки')
    expect(within(result).getByText(/ABS-1042/)).toBeInTheDocument()
    expect(within(result).getByText('На согласовании')).toBeInTheDocument()
    expect(within(result).getByText('Сохранено в журнале действий')).toBeInTheDocument()
    expect(spy).toHaveBeenCalledWith({ queryKey: ['plan'] })
    expect(spy).toHaveBeenCalledWith({ queryKey: ['actions'] })
    expect(await screen.findByText(/ABS-1042: Отгул/)).toBeInTheDocument()
    expect(screen.getByLabelText('Сообщение')).toBeEnabled() // done unlocks the input

    // reload: a fresh page gets the request from GET /api/actions
    unmount()
    mount()
    expect(await screen.findByText(/ABS-1042: Отгул/)).toBeInTheDocument()
  })

  it('R79: a double click on "Отправить" creates exactly one request', async () => {
    mount()
    await ask('хочу отгул в пятницу')
    const draft = await screen.findByLabelText('Черновик заявки')
    const btn = within(draft).getByRole('button', { name: 'Отправить' })
    await userEvent.dblClick(btn)
    await screen.findByLabelText('Результат заявки')
    expect(await screen.findAllByText(/ABS-\d+: Отгул/)).toHaveLength(1)
  })

  it('S3: cancelling the draft sends nothing', async () => {
    mount()
    await ask('хочу отгул в пятницу')
    const draft = await screen.findByLabelText('Черновик заявки')
    await userEvent.click(within(draft).getByRole('button', { name: 'Отменить' }))
    expect(await screen.findByText(/ничего не отправлено/)).toBeInTheDocument()
    expect(screen.queryByLabelText('Результат заявки')).not.toBeInTheDocument()
    expect(screen.getByText('Заявок пока нет.')).toBeInTheDocument()
  })

  it('R79: a retryable error shows "Повторить" and the retry succeeds', async () => {
    mount()
    await ask('ошибка при ответе')
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Сервис ответов временно недоступен')
    await userEvent.click(within(alert).getByRole('button', { name: 'Повторить' }))
    expect(await screen.findByText(/тестовый ответ прототипа/i)).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.getAllByText('ошибка при ответе')).toHaveLength(1) // the user bubble is not duplicated
  })

  it('R69: any other message answers with a test source and the badge', async () => {
    mount()
    await userEvent.click(screen.getByRole('button', { name: 'Кто мой наставник?' }))
    expect(await screen.findByText('Первая неделя стажёра')).toBeInTheDocument()
    expect(screen.getByText('Тестовая инструкция прототипа')).toBeInTheDocument()
  })

  it('shows an error block when the request is rejected', async () => {
    useSession.setState({ token: 'bad', user: null })
    mount()
    await ask('привет')
    expect((await screen.findAllByRole('alert')).length).toBeGreaterThan(0)
  })
})
