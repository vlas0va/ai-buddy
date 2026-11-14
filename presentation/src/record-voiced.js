// Запись видео-демо с озвучкой: один проход по сценариям на свежем стеке (http://127.0.0.1:5173, stub-LLM).
// Пишет video/demo.webm и video/scenes.json (время начала каждой сцены, как в ../voice/narration.json);
// склейку с голосом и субтитрами делает build-video.py.
const { chromium } = require('playwright')
const fs = require('fs')

const W = process.env.WEB_URL || 'http://127.0.0.1:5173'
const DIR = __dirname + '/video/'
const SIZE = { width: 1600, height: 900 }
const MONTHS = ['января', 'февраля', 'марта', 'апреля', 'мая', 'июня', 'июля', 'августа', 'сентября', 'октября', 'ноября', 'декабря']

// Отгул во вторник через 3 недели: в тот же день отсутствует и наставник Олег (руководитель увидит это в согласованиях).
const day = new Date()
day.setDate(day.getDate() + 21)
while (day.getDay() !== 2) day.setDate(day.getDate() + 1)
const dayLabel = `${day.getDate()} ${MONTHS[day.getMonth()]} ${day.getFullYear()}`
const dayText = `${String(day.getDate()).padStart(2, '0')}.${String(day.getMonth() + 1).padStart(2, '0')}.${day.getFullYear()}`
const QUESTION = 'Можно ли приходить в офис с собакой?'

// Длительность озвучки сцен (../voice/scene_<id>.mp3): сцена в записи длится не меньше голоса,
// чтобы в видео не было стоп-кадров. Без mp3 сцены идут в своём темпе.
const { execFileSync } = require('child_process')
const VOICE = __dirname + '/../voice/'
const LEAD_IN = 0.35
const TAIL = 0.6
function voiceLength(id) {
  const f = VOICE + `scene_${id}.mp3`
  if (!fs.existsSync(f)) return 0
  return parseFloat(execFileSync('ffprobe', ['-v', 'error', '-show_entries', 'format=duration', '-of', 'csv=p=0', f]).toString())
}

// Курсор в кадре: Playwright его не рисует, поэтому точка следует за мышью и «пульсирует» на клике.
const CURSOR = () => {
  window.addEventListener('DOMContentLoaded', () => {
    const c = document.createElement('div')
    c.style.cssText = 'position:fixed;z-index:2147483647;width:22px;height:22px;margin:-11px 0 0 -11px;border-radius:50%;' +
      'background:rgba(0,170,255,.35);border:2px solid #00aaff;pointer-events:none;left:-40px;top:-40px;transition:transform .12s'
    document.body.appendChild(c)
    addEventListener('mousemove', (e) => { c.style.left = e.clientX + 'px'; c.style.top = e.clientY + 'px' }, true)
    addEventListener('mousedown', () => { c.style.transform = 'scale(.6)' }, true)
    addEventListener('mouseup', () => { c.style.transform = 'scale(1)' }, true)
  })
}

async function waitIdle(p) {
  await p.waitForFunction(() => {
    const t = document.querySelector('[aria-label="Сообщение"]')
    return !t || !t.disabled
  }, null, { timeout: 60000 })
}

async function seedOlegDayOff(browser) {
  const ctx = await browser.newContext({ viewport: SIZE, locale: 'ru-RU' })
  const p = await ctx.newPage()
  await p.goto(W + '/login')
  await p.getByLabel('Логин').fill('mentor.oleg')
  await p.getByLabel('Пароль', { exact: true }).fill('demo')
  await p.getByRole('button', { name: 'Войти' }).click()
  await p.waitForURL('**/chat')
  await p.getByRole('button', { name: 'Новый диалог' }).click()
  await p.getByLabel('Сообщение').fill(`хочу отгул ${dayText}`)
  await p.getByLabel('Сообщение').press('Enter')
  await waitIdle(p)
  await p.getByLabel('Черновик заявки').last().getByRole('button', { name: 'Отправить' }).click()
  await p.getByLabel('Результат заявки').last().waitFor()
  await ctx.close()
}

;(async () => {
  fs.rmSync(DIR, { recursive: true, force: true })
  const b = await chromium.launch()
  await seedOlegDayOff(b)

  const ctx = await b.newContext({ viewport: SIZE, recordVideo: { dir: DIR, size: SIZE }, locale: 'ru-RU' })
  await ctx.grantPermissions(['clipboard-read', 'clipboard-write'], { origin: W })
  await ctx.addInitScript(CURSOR)
  const t0 = Date.now()
  const p = await ctx.newPage()
  const scenes = []
  const pause = (ms) => p.waitForTimeout(ms)
  const mouse = { x: SIZE.width / 2, y: SIZE.height / 2 }
  // Дотягивает предыдущую сцену до длины её озвучки: курсор медленно «гуляет», экран живой.
  const hold = async () => {
    const last = scenes[scenes.length - 1]
    if (!last) return
    const until = t0 + (last.t + LEAD_IN + voiceLength(last.id) + TAIL) * 1000
    let dir = 1
    while (Date.now() < until - 150) {
      const step = Math.min(1200, until - Date.now())
      mouse.x += dir * 24
      mouse.y += dir * 10
      dir = -dir
      await p.mouse.move(mouse.x, mouse.y, { steps: 12 })
      await pause(Math.max(0, step - 200))
    }
  }
  const scene = async (id) => {
    await hold()
    scenes.push({ id, t: (Date.now() - t0) / 1000 })
  }
  const hover = async (loc) => {
    await loc.scrollIntoViewIfNeeded()
    await loc.hover()
    const b = await loc.boundingBox()
    if (b) { mouse.x = b.x + b.width / 2; mouse.y = b.y + Math.min(b.height / 2, 120) }
    await pause(500)
  }

  const type = async (text) => {
    const i = p.getByLabel('Сообщение')
    await i.click()
    await i.pressSequentially(text, { delay: 45 })
    await pause(350)
    await i.press('Enter')
    await pause(400)
    await waitIdle(p)
    await pause(700)
  }
  const login = async (u) => {
    await p.goto(W + '/login')
    await p.getByLabel('Логин').click()
    await p.getByLabel('Логин').pressSequentially(u, { delay: 45 })
    await p.getByLabel('Пароль', { exact: true }).fill('demo')
    await pause(250)
    await p.getByRole('button', { name: 'Войти' }).click()
    await p.waitForURL('**/chat')
    await pause(500)
  }
  const logout = async () => {
    await p.getByRole('button', { name: 'Выйти' }).click()
    await p.waitForURL('**/login')
  }
  const nav = async (name) => {
    await p.getByRole('link', { name, exact: true }).click()
    await pause(900)
  }

  // 01 — экран входа с персонажем
  await p.goto(W + '/login')
  await pause(300)
  await scene('01-intro')
  await hover(p.getByText('Помощник для первых недель стажировки'))
  await pause(2500)

  // 02 — план стажёра
  await scene('02-plan')
  await login('intern.anna')
  await nav('Мой план')
  await p.getByRole('progressbar', { name: 'Прогресс плана' }).waitFor()
  await pause(1200)
  await p.mouse.wheel(0, 420)
  await pause(1500)
  await p.mouse.wheel(0, -420)
  await pause(800)

  // 03 — вопрос с источником
  await scene('03-ask')
  await nav('Чат')
  await p.getByRole('button', { name: 'Новый диалог' }).click()
  await type('Как оформить отгул?')
  const chip = p.getByLabel('Источники').last()
  if (await chip.count()) await hover(chip)
  await pause(1200)

  // 04 — отгул через календарь → черновик → заявка
  await scene('04-dayoff')
  const card = p.getByLabel('Выбор дат', { exact: true }).last()
  await card.getByRole('button', { name: /Выбери даты/ }).click()
  await pause(600)
  for (let i = 0; i < 14 && !(await card.getByRole('button', { name: new RegExp(`^${dayLabel}`) }).count()); i++) {
    await card.getByRole('button', { name: 'Следующий месяц' }).click()
    await pause(400)
  }
  await card.getByRole('button', { name: new RegExp(`^${dayLabel}`) }).click()
  await pause(500)
  await card.getByRole('button', { name: 'Один день' }).click()
  await pause(500)
  await card.getByRole('button', { name: 'Выбрать даты' }).click()
  await pause(400)
  await waitIdle(p)
  const draft = p.getByLabel('Черновик заявки').last()
  await hover(draft)
  await pause(1500)
  await draft.getByRole('button', { name: 'Отправить' }).click()
  await p.getByLabel('Результат заявки').last().waitFor()
  await hover(p.getByLabel('Результат заявки').last())
  await pause(1800)

  // 05 — доступы одним списком
  await scene('05-access')
  await p.getByRole('button', { name: 'Новый диалог' }).click()
  await type('Какие доступы мне нужны?')
  await hover(p.getByText('Недостающие доступы').last())
  await pause(2500)

  // 06 — вопрос, которого нет в базе знаний
  await scene('06-unknown')
  await p.getByRole('button', { name: 'Новый диалог' }).click()
  await type(QUESTION)
  const msg = p.getByLabel('Черновик сообщения').last()
  await hover(msg)
  await msg.getByRole('button', { name: 'Скопировать' }).click()
  await pause(2500)

  // 07 — наставник отвечает из списка, ответ — в базу знаний для всех
  await scene('07-mentor')
  await logout()
  await login('mentor.oleg')
  await nav('Вопросы стажёров')
  const q = p.locator('.esc-card').filter({ hasText: 'собак' }).first()
  await hover(q)
  await q.getByRole('textbox').click()
  await q.getByRole('textbox').pressSequentially('Нет, с животными в офис нельзя, кроме собак-поводырей.', { delay: 30 })
  await q.getByLabel(/Всем стажёрам/).check()
  await pause(700)
  await q.getByRole('button', { name: /Ответить/ }).click()
  await pause(1800)

  // 08 — стажёр другой команды получает ответ наставника
  await scene('08-reuse')
  await logout()
  await login('intern.ivan')
  await p.getByRole('button', { name: 'Новый диалог' }).click()
  await type(QUESTION)
  await hover(p.getByText('Ответ наставника').last())
  await pause(2200)

  // 08b — цели после встречи 1:1: стажёр записывает, наставник согласует
  await scene('08b-goals')
  await logout()
  await login('intern.anna')
  await nav('Мой план')
  const goals = p.getByLabel('Цели адаптации', { exact: true })
  const write = goals.getByRole('button', { name: 'Записать цели после встречи' })
  await hover(write)
  await write.click()
  await pause(500)
  await goals.getByLabel('Цель 1').click()
  await goals.getByLabel('Цель 1').pressSequentially('Самостоятельно выкатить задачу в прод', { delay: 30 })
  await goals.getByLabel('Критерий 1').click()
  await goals.getByLabel('Критерий 1').pressSequentially('Задача прошла ревью и выкачена без помощи наставника', { delay: 25 })
  await pause(500)
  await goals.getByRole('button', { name: 'Отправить на согласование' }).click()
  await goals.getByText('Ждёт согласования').first().waitFor()
  await hover(goals.getByText('Ждёт согласования').first())
  await pause(1200)
  await logout()
  await login('mentor.oleg')
  await nav('Мои стажёры')
  await p.getByRole('link', { name: /План: Анна/ }).first().click()
  const mGoals = p.getByLabel('Цели адаптации', { exact: true })
  const approve = mGoals.getByRole('button', { name: 'Согласовать' }).first()
  await hover(approve)
  await approve.click()
  await mGoals.getByText('В работе').first().waitFor()
  await hover(mGoals.getByText('В работе').first())
  await pause(1500)

  // 09 — польза buddy и отчёт за стажировку
  await scene('09-benefit')
  await nav('Мои стажёры')
  await hover(p.getByLabel('Польза buddy'))
  await pause(2500)
  await p.goto(W + '/team/intern.anna/report')
  await p.getByRole('heading', { name: /Отчёт за стажировку/ }).waitFor()
  await pause(1500)
  await p.mouse.wheel(0, 600)
  await pause(1800)

  // 10 — руководитель согласует, видит отсутствующих в те же дни; затем HR-администратор
  await scene('10-lead')
  await logout()
  await login('lead.maria')
  await p.getByRole('link', { name: 'Согласования', exact: true }).click()
  const appr = p.getByLabel(/Заявка: Анна Котова/).first()
  await appr.waitFor()
  await hover(appr.getByText(/В эти дни из команды отсутствуют/))
  await pause(1500)
  await appr.getByRole('button', { name: 'Согласовать' }).click()
  await pause(1500)
  await logout()
  await login('hradmin.olga')
  await p.getByRole('link', { name: 'Согласования', exact: true }).click()
  const hr = p.getByLabel(/Заявка: Анна Котова/).first()
  await hr.waitFor()
  await pause(800)
  await hr.getByRole('button', { name: 'Согласовать' }).click()
  await pause(1500)

  // 11 — финал на экране входа
  await scene('11-outro')
  await logout()
  await pause(3500)

  await hold()
  const end = (Date.now() - t0) / 1000
  const video = p.video()
  await ctx.close()
  await b.close()
  fs.renameSync(await video.path(), DIR + 'demo.webm')
  fs.writeFileSync(DIR + 'scenes.json', JSON.stringify({ scenes, end }, null, 2))
  console.log('scenes:', scenes.map((s) => `${s.id}@${s.t.toFixed(1)}`).join(' '), 'end', end.toFixed(1))
})().catch((e) => { console.error(e); process.exit(1) })
