/// <reference types="vitest/config" />
import { fileURLToPath } from 'node:url'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

export default defineConfig(({ mode }) => ({
  plugins: [react()],
  // Service worker MSW лежит в public-mock/ и раздаётся только в `npm run dev:mock`, в dist/ никогда не копируется.
  publicDir: mode === 'mock' ? 'public-mock' : false,
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  server: { port: 5173 },
  test: {
    environment: 'jsdom',
    // jsdom на каждый форкнутый воркер исчерпывал память на машинах разработчиков (exit 134); потоки с небольшим пулом
    // стабильны
    pool: 'threads',
    maxWorkers: 2,
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
    css: false,
    coverage: {
      provider: 'v8',
      include: ['src/**/*.{ts,tsx}'],
      exclude: ['src/**/*.test.{ts,tsx}', 'src/test/**', 'src/main.tsx', 'src/vite-env.d.ts'],
      reporter: ['text-summary', 'cobertura', 'html'],
      thresholds: { lines: 80 },
    },
  },
}))
