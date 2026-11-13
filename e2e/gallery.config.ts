import { defineConfig } from '@playwright/test'
import base from './playwright.config'

// README gallery: `npx playwright test --config gallery.config.ts` against a fresh stack writes docs/screenshots/*.png.
export default defineConfig({
  ...base,
  testDir: './gallery',
  timeout: 180_000,
  projects: [{ name: 'gallery', use: { browserName: 'chromium', viewport: { width: 1360, height: 860 }, deviceScaleFactor: 2 } }],
})
