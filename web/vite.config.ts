/// <reference types="vitest/config" />
import { fileURLToPath } from 'node:url'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

export default defineConfig(({ mode }) => ({
  plugins: [react()],
  // The MSW service worker lives in public-mock/ and is served only by `npm run dev:mock`, never copied into dist/.
  publicDir: mode === 'mock' ? 'public-mock' : false,
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  server: { port: 5173 },
  test: {
    environment: 'jsdom',
    // jsdom per forked worker exhausted memory on dev machines (exit 134); threads with a small pool are stable
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
