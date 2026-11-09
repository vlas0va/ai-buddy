import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { useSession } from '@/store/session'
import { jsonResponse } from '@/test/helpers'
import { TEST_ACCOUNTS } from './accounts'
import { LoginPage } from './LoginPage'

function setup(onLoggedIn = vi.fn()) {
  const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <LoginPage onLoggedIn={onLoggedIn} />
    </QueryClientProvider>,
  )
  return onLoggedIn
}

describe('LoginPage', () => {
  it('lists the test accounts and fills the form from one', async () => {
    setup()
    expect(TEST_ACCOUNTS.map((a) => a.login)).toContain('intern.anna')
    await userEvent.click(screen.getByRole('button', { name: 'mentor.oleg' }))
    expect(screen.getByLabelText('Логин')).toHaveValue('mentor.oleg')
    expect(screen.getByLabelText('Пароль')).toHaveValue('demo')
  })

  it('stores the session after a successful login', async () => {
    const user = { id: 'u1', login: 'intern.anna', display_name: 'Анна', role: 'intern', team_id: 'delivery' }
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse({ token: 'jwt', user })))
    const onLoggedIn = setup()
    await userEvent.click(screen.getByRole('button', { name: 'intern.anna' }))
    await userEvent.click(screen.getByRole('button', { name: 'Войти' }))
    await waitFor(() => expect(onLoggedIn).toHaveBeenCalled())
    expect(useSession.getState().token).toBe('jwt')
  })

  it('shows the error message on 401', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(jsonResponse({ code: 'unauthorized', message: 'Неверный логин или пароль', retryable: false }, 401)),
    )
    const onLoggedIn = setup()
    await userEvent.type(screen.getByLabelText('Логин'), 'x')
    await userEvent.type(screen.getByLabelText('Пароль'), 'y')
    await userEvent.click(screen.getByRole('button', { name: 'Войти' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Неверный логин или пароль')
    expect(onLoggedIn).not.toHaveBeenCalled()
  })
})
