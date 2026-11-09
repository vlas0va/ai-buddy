import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
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

  it('groups the test accounts under headings', () => {
    setup()
    for (const title of ['Стажёры', 'Команда', 'Другие роли']) {
      expect(screen.getByRole('heading', { name: title })).toBeInTheDocument()
    }
    const interns = within(screen.getByRole('region', { name: 'Стажёры' }))
    expect(interns.getByRole('button', { name: 'intern.anna' })).toBeInTheDocument()
    expect(interns.getByRole('button', { name: 'intern.ivan' })).toBeInTheDocument()
    expect(interns.queryByRole('button', { name: 'mentor.oleg' })).not.toBeInTheDocument()
  })

  it('clicking the card body only fills the form and marks it selected', async () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    setup()
    const card = screen.getByRole('button', { name: 'intern.ivan' })
    await userEvent.click(card)
    expect(fetchMock).not.toHaveBeenCalled()
    expect(card).toHaveAttribute('aria-pressed', 'true')
    expect(card.closest('li')).toHaveClass('account-selected')
  })

  it('toggles password visibility', async () => {
    setup()
    const input = screen.getByLabelText('Пароль')
    expect(input).toHaveAttribute('type', 'password')
    await userEvent.click(screen.getByRole('button', { name: 'Показать пароль' }))
    expect(input).toHaveAttribute('type', 'text')
    await userEvent.click(screen.getByRole('button', { name: 'Скрыть пароль' }))
    expect(input).toHaveAttribute('type', 'password')
  })

  it('shows a friendly message when the server is unreachable', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')))
    setup()
    await userEvent.type(screen.getByLabelText('Логин'), 'x')
    await userEvent.type(screen.getByLabelText('Пароль'), 'y')
    await userEvent.click(screen.getByRole('button', { name: 'Войти' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Сервер недоступен. Проверьте, что запущен бэкенд, или включите демо-режим.',
    )
  })
})
