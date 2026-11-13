import { defineConfig } from '@playwright/test'
import base from './playwright.config'

// Галерея README: `npx playwright test --config gallery.config.ts` на свежем стеке пишет docs/screenshots/*.png.
export default defineConfig({
  ...base,
  testDir: './gallery',
  timeout: 180_000,
  projects: [{ name: 'gallery', use: { browserName: 'chromium', viewport: { width: 1360, height: 860 }, deviceScaleFactor: 2 } }],
})
