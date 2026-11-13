import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterEach, vi } from 'vitest'

// jsdom не реализует scrollTo; роутер вызывает его при навигации
window.scrollTo = () => {}

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  try {
    localStorage.clear()
  } catch {
    /* хранилище недоступно */
  }
})
