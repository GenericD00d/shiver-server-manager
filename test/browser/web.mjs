// Logs into the manager's web page with a real OS account, checks the
// servers are listed, runs a real action, and logs out.
//
//   node web.mjs <user> <password>   (the page on http://localhost:8443)
import { chromium } from 'playwright';

const [user, password] = process.argv.slice(2);
const base = 'http://localhost:8443';
const browser = await chromium.launch();
const page = await browser.newPage();
let failed = false;
const check = (ok, what) => {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${what}`);
  if (!ok) failed = true;
};
try {
  await page.goto(base);
  await page.fill('#username', user);
  await page.fill('#password', 'not the password');
  await page.click('button[type=submit]');
  await page.waitForSelector('text=wrong username or password', { timeout: 15_000 });
  check(true, 'a wrong password is refused');

  await page.fill('#password', password);
  await page.click('button[type=submit]');
  await page.waitForSelector('text=Your servers', { timeout: 30_000 });
  check(true, 'logged in through PAM');
  await page.waitForSelector('td:has-text("chat-a")', { timeout: 60_000 });
  check(true, 'servers are listed');

  await page.click('button[title="Actions for chat-a"]');
  await page.click('[role=menuitem]:has-text("Restart")');
  await page.click('button:has-text("Restart chat-a")');
  await page.waitForSelector('text=Done.', { timeout: 120_000 });
  const output = await page.locator('.font-mono').first().innerText();
  check(output.includes('restart chat-a'), 'a restart ran and streamed its output');
  await page.click('button:has-text("Close")');

  for (const [hash, text] of [['certificates', 'days left'], ['storage', '/var/lib/sharkord'], ['firewall', 'caddy'], ['security', user]]) {
    await page.goto(`${base}/#/${hash}`);
    await page.waitForSelector(`text=${text}`, { timeout: 60_000 });
    check(true, `${hash} page shows ${text}`);
  }

  const status = await page.evaluate(async () => (await fetch('/api/actions/system.health', { method: 'POST', body: '{}' })).status);
  check(status === 403, 'a request without the app header is refused');

  await page.click('button[title="Log out"]');
  await page.waitForSelector('#username');
  check(true, 'logged out');
} catch (e) {
  failed = true;
  console.error(e);
  await page.screenshot({ path: 'web-failure.png', fullPage: true });
} finally {
  await browser.close();
}
console.log(failed ? 'WEB TEST FAILED' : 'WEB TEST PASSED');
process.exit(failed ? 1 : 0);
