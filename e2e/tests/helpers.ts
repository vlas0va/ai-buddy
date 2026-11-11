import { expect, type Page } from '@playwright/test'

export const API_URL = process.env.API_URL ?? 'http://localhost:8080'

/** Logs in through the real form (synthetic accounts, password «demo»). */
export async function login(page: Page, user: string) {
  await page.goto('/login')
  await page.getByLabel('Логин').fill(user)
  await page.getByLabel('Пароль', { exact: true }).fill('demo')
  await page.getByRole('button', { name: 'Войти' }).click()
  await page.waitForURL('**/chat')
}

/** Starts a clean conversation and sends one message. */
export async function newDialog(page: Page) {
  await page.getByRole('button', { name: 'Новый диалог' }).click()
}

export async function ask(page: Page, text: string) {
  const input = page.getByLabel('Сообщение')
  await expect(input).toBeEnabled()
  await input.fill(text)
  await input.press('Enter')
}

/** Waits until buddy finished the turn (input unlocked again). */
export async function waitAnswer(page: Page) {
  await expect(page.getByLabel('Сообщение')).toBeEnabled({ timeout: 30_000 })
}

export function lastTurn(page: Page) {
  return page.locator('.chat-turn').last()
}

/** A Tuesday 3–10 weeks ahead, different on every run, so repeated runs do not overlap earlier requests. */
export function futureTuesday(seed = Date.now()): Date {
  const d = new Date()
  d.setHours(12, 0, 0, 0)
  d.setDate(d.getDate() + 21 + ((Math.floor(seed / 1000) % 8) * 7))
  while (d.getDay() !== 2) d.setDate(d.getDate() + 1)
  return d
}

export const ddmmyyyy = (d: Date) =>
  `${String(d.getDate()).padStart(2, '0')}.${String(d.getMonth() + 1).padStart(2, '0')}.${d.getFullYear()}`
