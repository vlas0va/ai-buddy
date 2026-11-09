import { QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { createQueryClient } from './queryClient'
import { createAppRouter } from './router'
import { useSession } from '@/store/session'
import { jsonResponse } from '@/test/helpers'

const user = { id: 'u1', login: 'intern.anna', display_name: 'Анна', role: 'intern', team_id: 'delivery' }

function mount(path: string) {
  const router = createAppRouter(createMemoryHistory({ initialEntries: [path] }))
  render(
    <QueryClientProvider client={createQueryClient()}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  return router
}

describe('routing and auth guard', () => {
  it('redirects an anonymous visitor to /login', async () => {
    const router = mount('/plan')
    expect(await screen.findByRole('heading', { name: 'Вход в AI-buddy' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/login')
  })

  it('signed-in user lands on /chat, sees role, and can log out', async () => {
    useSession.getState().setSession('tok', user as never)
    vi.stubGlobal(
      'fetch',
      vi.fn((url: string) =>
        Promise.resolve(url.endsWith('/api/me') ? jsonResponse({ user, flags: {} }) : jsonResponse([])),
      ),
    )
    const router = mount('/')
    expect(await screen.findByRole('heading', { name: 'Чат с buddy' })).toBeInTheDocument()
    expect(await screen.findByText('Стажёр')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Выйти' }))
    expect(await screen.findByRole('heading', { name: 'Вход в AI-buddy' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/login')
    expect(useSession.getState().token).toBeNull()
  })

  it('signed-in user visiting /login is sent to /chat', async () => {
    useSession.getState().setSession('tok', user as never)
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse([])))
    const router = mount('/login')
    expect(await screen.findByRole('heading', { name: 'Чат с buddy' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/chat')
  })
})
