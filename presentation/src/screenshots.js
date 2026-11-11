const { chromium } = require('playwright');
const OUT = __dirname + '/img/';
const W = 'http://127.0.0.1:5173';
(async () => {
  const b = await chromium.launch();
  const mk = async () => (await b.newContext({ viewport: { width: 1280, height: 800 }, deviceScaleFactor: 2, locale: 'ru-RU' })).newPage();
  const login = async (p, u) => { await p.goto(W + '/login'); await p.getByLabel('Логин').fill(u); await p.getByLabel('Пароль', { exact: true }).fill('demo'); await p.getByRole('button', { name: 'Войти' }).click(); await p.waitForURL('**/chat'); };
  const ask = async (p, t) => { const i = p.getByLabel('Сообщение'); await i.fill(t); await i.press('Enter'); await p.waitForTimeout(300); await p.getByLabel('Сообщение').waitFor(); await p.waitForFunction(() => !document.querySelector('textarea[aria-label="Сообщение"]')?.disabled, null, { timeout: 30000 }); await p.waitForTimeout(400); };
  const fresh = async (p) => { await p.getByRole('button', { name: 'Новый диалог' }).click(); await p.waitForTimeout(300); };
  // pick a Thursday 2-3 weeks ahead for the demo
  const d = new Date(); d.setDate(d.getDate() + 15); while (d.getDay() !== 4) d.setDate(d.getDate() + 1);
  const day = `${String(d.getDate()).padStart(2,'0')}.${String(d.getMonth()+1).padStart(2,'0')}.${d.getFullYear()}`;

  const a = await mk(); await login(a, 'intern.anna');
  await fresh(a); await ask(a, 'Как оформить отгул?'); await a.screenshot({ path: OUT + 's3-howto.png' });
  await ask(a, `хочу отгул ${day}`); await a.screenshot({ path: OUT + 's3-draft.png' });
  await a.getByLabel('Черновик заявки').last().getByRole('button', { name: 'Отправить' }).click();
  await a.getByLabel('Результат заявки').last().waitFor(); await a.waitForTimeout(800);
  await a.screenshot({ path: OUT + 's3-result.png' });
  const l = await mk(); await login(l, 'lead.maria'); await l.getByRole('link', { name: 'Согласования' }).click();
  await l.locator('.approval-card').first().waitFor(); await l.screenshot({ path: OUT + 's3-approvals.png' });
  await l.locator('.approval-card').filter({ hasText: day.slice(0,5) }).first().getByRole('button', { name: 'Согласовать' }).click();
  await l.waitForTimeout(1200);
  await a.reload(); await a.getByRole('complementary', { name: 'Мои заявки' }).waitFor(); await a.waitForTimeout(800);
  await a.screenshot({ path: OUT + 's3-approved.png' });

  await fresh(a); await ask(a, 'что дальше?'); await a.screenshot({ path: OUT + 's1-chat.png' });
  await a.goto(W + '/plan'); await a.getByText('Готово').first().waitFor(); await a.waitForTimeout(500); await a.screenshot({ path: OUT + 's1-plan.png' });
  await a.goto(W + '/chat'); await fresh(a); await ask(a, 'Как у нас проходит код-ревью?'); await a.screenshot({ path: OUT + 's2-answer.png' });
  await fresh(a); await ask(a, 'где получить ноутбук?'); await a.screenshot({ path: OUT + 's2-laptop.png' });
  await fresh(a); await ask(a, 'Где посмотреть расписание командного митапа по архитектуре?'); await a.screenshot({ path: OUT + 's2-escalate.png' });
  await fresh(a); await ask(a, 'какие доступы мне нужны?'); await a.screenshot({ path: OUT + 's4-access.png' });
  await fresh(a); await ask(a, 'напиши за меня код задачи целиком'); await a.screenshot({ path: OUT + 'co-learning.png' });
  await fresh(a); await ask(a, 'ERROR: Permission denied (publickey).\nfatal: Could not read from remote repository.\npassword=hunter2'); await a.screenshot({ path: OUT + 'co-log.png' });
  await fresh(a); await ask(a, 'сколько я буду получать?'); await ask(a, 'покажи отпуск Ивана'); await a.screenshot({ path: OUT + 'guard.png' });
  const m = await mk(); await login(m, 'mentor.oleg'); await m.goto(W + '/team'); await m.getByRole('link', { name: /План:/ }).first().waitFor(); await m.waitForTimeout(400);
  await m.screenshot({ path: OUT + 'mentor-team.png' });
  await m.getByRole('link', { name: /План:/ }).first().click(); await m.getByText('Цели адаптации').waitFor();
  await m.evaluate(() => window.scrollTo(0, document.body.scrollHeight)); await m.waitForTimeout(400); await m.screenshot({ path: OUT + 'mentor-goals.png' });
  await b.close();
})().catch(e => { console.error(e); process.exit(1); });
