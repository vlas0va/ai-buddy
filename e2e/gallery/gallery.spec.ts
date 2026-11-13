import { expect, type Browser, type Page, test } from '@playwright/test'
import { ask, ddmmyyyy, futureTuesday, lastTurn, login, newDialog, waitAnswer } from '../tests/helpers'

// Скриншоты для галереи README, по одному сценарию на роль на свежем стеке (stub-LLM).
const OUT = '../docs/screenshots'
const shot = (page: Page, name: string) => page.screenshot({ path: `${OUT}/${name}.png` })

async function as(browser: Browser, user: string) {
  const page = await (await browser.newContext()).newPage()
  await login(page, user)
  return page
}

test('gallery', async ({ browser }) => {
  const anon = await (await browser.newContext()).newPage()
  await anon.goto('/login')
  await expect(anon.getByRole('button', { name: 'Войти' })).toBeVisible()
  await shot(anon, '01-login')

  // Стажёр: вопрос с источниками, отметка «Полезно», отгул через черновик.
  const day = ddmmyyyy(futureTuesday())
  const anna = await as(browser, 'intern.anna')
  await newDialog(anna)
  await ask(anna, 'Как оформить отгул?')
  await waitAnswer(anna)
  await lastTurn(anna).getByRole('button', { name: 'Полезно', exact: true }).click()
  await ask(anna, `хочу отгул ${day}`)
  await waitAnswer(anna)
  await expect(anna.getByLabel('Черновик заявки').last()).toBeVisible()
  await shot(anna, '02-intern-chat-day-off')
  await anna.getByLabel('Черновик заявки').last().getByRole('button', { name: 'Отправить' }).click()
  await expect(anna.getByLabel('Результат заявки').last()).toContainText(/ABS-\d+/)

  await newDialog(anna)
  await ask(anna, 'Какие доступы мне нужны?')
  await waitAnswer(anna)
  await shot(anna, '03-intern-accesses')

  await newDialog(anna)
  await ask(anna, 'Где посмотреть расписание командного митапа по архитектуре в этом квартале?')
  await waitAnswer(anna)
  await expect(anna.getByLabel('Черновик сообщения').last()).toContainText('Вопрос уже в списке наставника')
  for (const q of ['кто мой наставник?', 'Как настроить VPN?', 'Как создать ветку и открыть PR?']) {
    await ask(anna, q)
    await waitAnswer(anna)
  }

  await anna.goto('/plan')
  await expect(anna.getByRole('progressbar', { name: 'Прогресс плана' })).toBeVisible()
  await shot(anna, '04-intern-plan')

  // Наставник: отгул в тот же день (руководитель видит, кого из команды не будет), команда с «Польза buddy», вопросы,
  // отчёт.
  const oleg = await as(browser, 'mentor.oleg')
  await newDialog(oleg)
  await ask(oleg, `хочу отгул ${day}`)
  await waitAnswer(oleg)
  await oleg.getByLabel('Черновик заявки').last().getByRole('button', { name: 'Отправить' }).click()
  await expect(oleg.getByLabel('Результат заявки').last()).toContainText(/ABS-\d+/)
  await oleg.goto('/team')
  await expect(oleg.getByLabel('Польза buddy').getByText(/вопросов стажёров buddy закрыл сам/)).toBeVisible()
  await shot(oleg, '05-mentor-team-benefit')
  await oleg.goto('/questions')
  await expect(oleg.locator('.esc-card').first()).toBeVisible()
  await shot(oleg, '06-mentor-questions')
  await oleg.goto('/team/intern.anna/report')
  await expect(oleg.getByRole('heading', { name: /Отчёт за стажировку/ })).toBeVisible()
  await shot(oleg, '07-mentor-report')

  // Руководитель: согласования с отсутствиями команды в эти дни.
  const maria = await as(browser, 'lead.maria')
  await maria.getByRole('link', { name: 'Согласования', exact: true }).click()
  await expect(maria.getByLabel(/Заявка: Анна Котова/).first()).toBeVisible()
  await expect(maria.getByText(/В эти дни из команды отсутствуют/).first()).toBeVisible()
  await shot(maria, '08-lead-approvals')
})
