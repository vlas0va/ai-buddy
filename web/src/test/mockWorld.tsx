import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render } from '@testing-library/react'
import type { ReactElement } from 'react'
import { MOCK_USERS, state } from '@/mocks/db'
import { confirmEvents, messageEvents } from '@/mocks/scenario'
import { useSession } from '@/store/session'

/** Помощники для тестов, которые запускают настоящие компоненты против обработчиков MSW из `npm run dev:mock`. */

export const userByLogin = (login: string) => MOCK_USERS.find((u) => u.login === login)!

export function signInAs(login: string) {
  const user = userByLogin(login)
  useSession.setState({ token: `mock-token-${login}`, user })
  return user
}

export function mountWithClient(ui: ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return { client, ...render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>) }
}

/** Создаёт одну подтверждённую заявку пользователя в обход интерфейса; возвращает id сохранённого действия. */
export function seedRequest(login: string, text: string) {
  const user = userByLogin(login)
  messageEvents('seed-session', user, text)
  const proposal = [...state.proposals.values()].at(-1)!
  confirmEvents(proposal)
  return state.actions.get(user.id)![0]!
}
