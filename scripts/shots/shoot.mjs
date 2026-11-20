// Screenshots of the seeded cafard, desktop and phone.
// Usage: node shoot.mjs <base> <password> <out>
import puppeteer from 'puppeteer-core';
const [base, password, out] = process.argv.slice(2);
const browser = await puppeteer.launch({ executablePath: '/usr/bin/chromium', args: ['--no-sandbox'] });
const page = await browser.newPage();
page.on('pageerror', (e) => console.log('pageerror', e.message));
page.on('console', (m) => { if (m.type() === 'error') console.log('console', m.text()); });
await page.goto(base + '/signin');
await page.type('input[name=password]', password);
await Promise.all([page.waitForNavigation(), page.keyboard.press('Enter')]);
const wait = (ms) => new Promise((r) => setTimeout(r, ms));
const shots = [
  ['/', 'home'],
  ['/projects/lab/c/dut1', 'chat-members'],
  ['/projects/lab/c/general', 'chat-general'],
  ['/projects/lab/g/shots', 'gallery'],
  ['/projects/lab/logs', 'logs'],
  ['/dm', 'dm-list'],
  ['/dm/codex-build', 'dm-chat'],
  ['/projects/~admin/c/standup', 'private-chat'],
  ['/channels/new', 'new-private'],
  ['/channels/new?project=lab&kind=gallery', 'new-gallery'],
];
for (const [w, h, tag] of [[1280, 800, 'desktop'], [390, 844, 'phone']]) {
  await page.setViewport({ width: w, height: h, deviceScaleFactor: tag === 'phone' ? 2 : 1, isMobile: tag === 'phone', hasTouch: tag === 'phone' });
  for (const [p, name] of shots) {
    await page.goto(base + p, { waitUntil: 'load' });
    await wait(700);
    await page.screenshot({ path: `${out}/${tag}-${name}.png` });
  }
  if (tag === 'phone') {
    await page.goto(base + '/projects/lab/c/dut1', { waitUntil: 'load' });
    await wait(500);
    await page.click('[data-action="open-side"]');
    await wait(500);
    await page.screenshot({ path: `${out}/phone-drawer.png` });
    await page.keyboard.press('Escape');
    await wait(300);
    await page.click('[data-action="members"]');
    await wait(700);
    await page.screenshot({ path: `${out}/phone-members-sheet.png` });
  } else {
    await page.goto(base + '/projects/lab/c/dut1', { waitUntil: 'load' });
    await wait(500);
    await page.click('[data-action="members"]');
    await wait(400);
    await page.screenshot({ path: `${out}/desktop-chat-members-hidden.png` });
    await page.click('[data-action="members"]');
    await wait(300);
  }
}
await browser.close();
