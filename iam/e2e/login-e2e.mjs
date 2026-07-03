import { chromium } from 'playwright';

const shot = (p, n) => p.screenshot({ path: `/tmp/e2e-${n}.png`, fullPage: true });

const browser = await chromium.launch();
const page = await browser.newPage();
try {
  // 1. Stub index → click Log in
  await page.goto('http://localhost:5556/');
  await shot(page, '1-stub');
  await page.click('text=Log in');

  // 2. Login v2: loginname
  await page.waitForSelector('input[name="loginName"], input[type="text"]', { timeout: 15000 });
  await shot(page, '2-loginname');
  await page.fill('input[name="loginName"], input[type="text"]', 'demo-admin');
  await page.click('button[type="submit"]');

  // 3. Password
  await page.waitForSelector('input[type="password"]', { timeout: 15000 });
  await shot(page, '3-password');
  await page.fill('input[type="password"]', 'Demo-Admin-Passw0rd!');
  await page.click('button[type="submit"]');

  // 4. Back at the stub, logged in
  await page.waitForURL('http://localhost:5556/**', { timeout: 20000 });
  await page.waitForSelector('text=Logged in as', { timeout: 10000 });
  await shot(page, '4-logged-in');
  const who = await page.textContent('p:has-text("Logged in as")');
  console.log('LOGIN OK:', who.trim());

  // 5. Logout round trip
  await page.click('text=Log out');
  await page.waitForSelector('text=Log in', { timeout: 15000 });
  await shot(page, '5-logged-out');
  console.log('LOGOUT OK, final url:', page.url());
} catch (e) {
  await shot(page, 'error');
  console.error('E2E FAILED at', page.url());
  console.error(String(e).split('\n')[0]);
  process.exitCode = 1;
} finally {
  await browser.close();
}
