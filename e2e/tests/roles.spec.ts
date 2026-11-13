import { expect, test } from '@playwright/test'
import { API_URL, login } from './helpers'

// Role views come from /api/me; the server enforces rights, the menu only reflects them.
const MENUS: Record<string, string[]> = {
  'intern.anna': ['Чат', 'Мой план', 'Мои вопросы'],
  'mentor.oleg': ['Чат', 'Мой план', 'Мои стажёры', 'Вопросы стажёров'],
  'lead.maria': ['Чат', 'Мои стажёры', 'Вопросы стажёров', 'Согласования'],
}

for (const [user, items] of Object.entries(MENUS)) {
  test(`menu for ${user}`, async ({ page }) => {
    await login(page, user)
    const nav = page.getByRole('navigation', { name: 'Основное меню' })
    for (const item of items) await expect(nav.getByRole('link', { name: item })).toBeVisible()
  })
}

test('an intern cannot open the approvals page', async ({ page }) => {
  await login(page, 'intern.anna')
  await page.goto('/approvals')
  await expect(page).toHaveURL(/\/chat$/)
})

test('«Польза buddy»: the mentor and the lead see the panel, the intern gets 403 (idea 13.11)', async ({ browser, request }) => {
  for (const user of ['mentor.oleg', 'lead.maria']) {
    const page = await (await browser.newContext()).newPage()
    await login(page, user)
    await page.goto('/team')
    const panel = page.getByLabel('Польза buddy')
    await expect(panel.getByRole('heading', { name: 'Польза buddy' })).toBeVisible()
    await expect(panel.getByText(/вопросов стажёров buddy закрыл сам|ещё не задавали вопросов buddy/)).toBeVisible()
  }
  const page = await (await browser.newContext()).newPage()
  await login(page, 'intern.anna')
  const token = await page.evaluate(() => /"token":"([^"]+)"/.exec(localStorage.getItem('ai-buddy-session') ?? '')?.[1] ?? '')
  expect(token).not.toBe('')
  const res = await request.get(`${API_URL}/api/team/benefit`, { headers: { Authorization: `Bearer ${token}` } })
  expect(res.status()).toBe(403)
})
