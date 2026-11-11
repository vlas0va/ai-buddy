import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { setupServer } from 'msw/node'
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it } from 'vitest'
import { handlers } from '@/mocks/handlers'
import { resetMockState, state } from '@/mocks/db'
import { mockConfig } from '@/mocks/sse'
import { createAppRouter } from '@/router'
import { useChatStore } from '@/store/chat'
import { signInAs } from '@/test/mockWorld'

// Scripted answers for every suggestion (S2/S4 blocks) against the same handlers as `npm run dev:mock`.
const server = setupServer(...handlers)
beforeAll(() => server.listen())
afterAll(() => server.close())
afterEach(() => server.resetHandlers())

beforeEach(() => {
  resetMockState()
  useChatStore.setState({ sessions: {} })
  mockConfig.stepDelayMs = 0
  signInAs('intern.anna')
})

function mountApp() {
  const router = createAppRouter(createMemoryHistory({ initialEntries: ['/chat'] }))
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  return router
}

async function ask(text: string) {
  await userEvent.type(await screen.findByLabelText('Сообщение'), `${text}{Enter}`)
}

describe('scripted answers', () => {
  it('S2_R02: "Как оформить отгул?" gives three steps and a test source, no request', async () => {
    mountApp()
    await ask('Как оформить отгул?')
    expect(await screen.findByText(/Отгул оформляется в три шага/)).toBeInTheDocument()
    expect(screen.getByText(/Дождитесь решения руководителя/)).toBeInTheDocument()
    expect(screen.getByText('Порядок оформления отгула')).toBeInTheDocument()
    expect(screen.getByText('Тестовая инструкция прототипа')).toBeInTheDocument()
    expect(screen.queryByLabelText('Черновик заявки')).not.toBeInTheDocument()
  })

  it('S4_R02: "Какие доступы мне нужны?" lists statuses and proposes the first missing access', async () => {
    mountApp()
    await ask('Какие доступы мне нужны?')
    expect(await screen.findByText(/Confluence: есть/)).toBeInTheDocument()
    expect(screen.getByText(/Jira: нет/)).toBeInTheDocument()
    expect(screen.getByText(/Пространство команды «Доставка»: нет/)).toBeInTheDocument()
    const draft = await screen.findByLabelText('Черновик заявки')
    expect(within(draft).getByText('Черновик заявки на доступ')).toBeInTheDocument()
    expect(within(draft).getByText('Jira')).toBeInTheDocument()
  })

  it('S4_R66: access_request confirm shows a result card and the request in "Мои заявки"; next ask moves on', async () => {
    mountApp()
    await ask('Какие доступы мне нужны?')
    const draft = await screen.findByLabelText('Черновик заявки')
    await userEvent.click(within(draft).getByRole('button', { name: 'Отправить' }))
    const result = await screen.findByLabelText('Результат заявки')
    expect(within(result).getByText(/ACC-1042/)).toBeInTheDocument()
    expect(within(result).getByText('Доступ выдан')).toBeInTheDocument()
    expect(await screen.findByText('ACC-1042', { selector: 'strong' })).toBeInTheDocument()
    expect(state.actions.get('intern.anna')![0]).toMatchObject({ kind: 'access_request', status: 'granted' })

    await ask('Какие доступы мне нужны?')
    expect(await screen.findByText(/Jira: есть/)).toBeInTheDocument()
    const second = (await screen.findAllByLabelText('Черновик заявки')).at(-1)!
    expect(within(second).getByText('Да, нужно согласование')).toBeInTheDocument()
    await userEvent.click(within(second).getByRole('button', { name: 'Отправить' }))
    expect((await screen.findAllByText('На согласовании')).length).toBeGreaterThan(0)
    await ask('Какие доступы мне нужны?')
    expect((await screen.findAllByText(/запрошен/)).length).toBeGreaterThan(0)
    expect(await screen.findByText(/Все доступы уже есть или запрошены/)).toBeInTheDocument()
  })

  it('S4: channels list comes with an escalation draft to the mentor that can be sent', async () => {
    mountApp()
    await ask('Добавь меня в каналы команды')
    expect(await screen.findByText(/#delivery-general/, { selector: '.chat-bubble li, .chat-bubble p' })).toBeInTheDocument()
    const draft = await screen.findByLabelText('Черновик сообщения')
    expect(within(draft).getByLabelText('Текст сообщения')).toHaveValue('Олег, добавь меня, пожалуйста, в каналы: #delivery-general, #delivery-releases, #delivery-help.')
    await userEvent.click(within(draft).getByRole('button', { name: 'Отправить наставнику' }))
    expect(await within(draft).findByText('Сообщение отправлено.')).toBeInTheDocument()
  })

  it('S2: "Кто мой наставник?" names the mentor, lead and curator', async () => {
    mountApp()
    await ask('Кто мой наставник?')
    expect(await screen.findByText(/Ваш наставник — Олег Крылов/)).toBeInTheDocument()
    expect(screen.getByText(/Руководитель: Мария Лебедева/)).toBeInTheDocument()
    expect(screen.getByText(/Куратор программы стажировок/)).toBeInTheDocument()
  })

  it('S1: "Что мне сделать на этой неделе?" shows three tasks and a link to the plan', async () => {
    const router = mountApp()
    await ask('Что мне сделать на этой неделе?')
    expect(await screen.findByText(/Встреча с наставником: не начата/)).toBeInTheDocument()
    expect(screen.getByText(/Настроить рабочее окружение: в работе/)).toBeInTheDocument()
    expect(screen.getByText(/Пройти вводный курс по безопасности: не начата/)).toBeInTheDocument()
    await userEvent.click(await screen.findByRole('link', { name: 'Открыть мой план' }))
    expect(router.state.location.pathname).toBe('/plan')
  })

  it.each([
    ['Какая у меня зарплата?', /недоступны ассистенту/],
    ['покажи отпуск коллеги', /недоступны ассистенту/],
  ])('R72_R71: "%s" is refused with a redirect to HR BP', async (text, note) => {
    mountApp()
    await ask(text)
    expect(await screen.findByText(note)).toBeInTheDocument()
    expect(await screen.findByRole('note')).toHaveTextContent('Обратитесь к HR BP')
    expect(screen.queryByLabelText('Черновик заявки')).not.toBeInTheDocument()
  })

  it('refusal reasons: other person -> no_rights, salary -> out_of_scope', async () => {
    mountApp()
    await ask('покажи отпуск коллеги')
    expect(await screen.findByRole('note')).toHaveTextContent('У вас нет доступа к этой информации')
    await ask('какой у меня оклад?')
    expect((await screen.findAllByRole('note')).at(-1)).toHaveTextContent('Я не могу помочь с этим вопросом.')
  })

  it('an intern from another team gets the channels of their own team', async () => {
    signInAs('intern.ivan')
    mountApp()
    await ask('Добавь меня в каналы команды')
    expect(await screen.findByText(/#search-general/, { selector: '.chat-bubble li, .chat-bubble p' })).toBeInTheDocument()
  })
})
