import { QueryClientProvider } from '@tanstack/react-query'
import { RouterProvider } from '@tanstack/react-router'
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { createQueryClient } from './queryClient'
import { createAppRouter } from './router'
import '@fontsource-variable/manrope'
import './styles/tokens.css'
import './styles/base.css'
import './components/ui/ui.css'
import './chat/chat.css'

async function bootstrap() {
  // Только для мок-режима разработки. Константа подставляется при сборке, поэтому динамический импорт убирается из
  // продакшен-сборки.
  if (import.meta.env.VITE_USE_MOCKS === 'true') {
    const { startMocks } = await import('./mocks/browser')
    await startMocks()
  }
  const router = createAppRouter()
  const queryClient = createQueryClient()

  createRoot(document.getElementById('root')!).render(
    <StrictMode>
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    </StrictMode>,
  )
}

void bootstrap()
