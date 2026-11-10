import { setupWorker } from 'msw/browser'
import { enableMockPersistence } from './db'
import { handlers } from './handlers'

export async function startMocks(): Promise<void> {
  enableMockPersistence()
  await setupWorker(...handlers).start({ quiet: true })
}
