const { chromium } = require('playwright');
(async () => {
  const b = await chromium.launch(); const p = await b.newPage({ viewport: { width: 1280, height: 720 } });
  await p.goto('file://' + __dirname + '/deck.html'); await p.waitForLoadState('networkidle');
  await p.pdf({ path: __dirname + '/../AI-buddy.pdf', width: '1280px', height: '720px', printBackground: true, preferCSSPageSize: true });
  for (const i of [1,4,10,12]) { await p.evaluate(n => document.querySelectorAll('.s')[n-1].scrollIntoView(), i); }
  await b.close();
})();
