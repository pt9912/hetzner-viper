import { expect, test } from '@playwright/test';
import { canFakeOpener, startUI, UI } from '../fixture';

let ui: UI;
// the key works once: every test has a server of its own
test.beforeEach(async () => { ui = await startUI(); });
test.afterEach(async () => { await ui.stop(); });

test('the one-time key becomes a session and the overview shows the machines', async ({ page }) => {
  const problems: string[] = [];
  page.on('console', (m) => { if (m.type() === 'error') { problems.push(m.text()); } });
  page.on('pageerror', (e) => problems.push(String(e)));
  page.on('response', (r) => { if (r.status() >= 400) { problems.push(r.status() + ' ' + r.url()); } });
  await page.goto(ui.start);
  await expect(page).toHaveURL(ui.base + '/');
  await expect(page.locator('#machines tbody tr')).toHaveCount(3); // web, a and b in the group dev
  await expect(page.locator('#groups tbody tr')).toHaveCount(1);
  await expect(page.locator('#machines tbody tr', { hasText: 'web' })).toContainText('compose');
  expect(problems).toEqual([]); // no CSP violation, no failed request
});

test('the key works once', async ({ browser }) => {
  const first = await browser.newContext();
  expect((await (await first.newPage()).goto(ui.start, { waitUntil: 'commit' }))?.status()).toBe(200); // the exchange, then the page
  const second = await browser.newContext();
  const again = await (await second.newPage()).goto(ui.start, { waitUntil: 'commit' }); // only the answer matters; a page that moves on by itself is never "loaded" for Playwright's Firefox
  expect(again?.status()).toBe(403);
  await first.close();
  await second.close();
});

test('another browser without the cookie gets nothing', async ({ browser }) => {
  const ctx = await browser.newContext();
  for (const p of ['/', '/api/overview', '/app.js']) {
    const res = await ctx.request.get(ui.base + p);
    expect(res.status()).toBe(403);
  }
  await ctx.close();
});

test('localhost is not the address: the host check refuses it', async ({ browser }) => {
  // (a page of its own, in a context of its own: a move from the page of 127.0.0.1 to another origin makes the browser change
  // the browsing context group because of the COOP header, which Playwright's WebKit answers with an internal assertion)
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  const res = await page.goto(`http://localhost:${new URL(ui.base).port}/`).catch(() => null);
  expect(res === null || res.status() === 403).toBeTruthy();
  await ctx.close();
});

test('a page of another origin cannot start anything', async ({ page, context }) => {
  await page.goto(ui.start); // the session cookie is in this browser now
  // a second origin (the test server of Playwright answers on another port): its script posts to viper ui
  await context.route('http://evil.test/**', (route) => route.fulfill({ contentType: 'text/html', body: '<html><body>evil</body></html>' }));
  const evil = await context.newPage();
  await evil.goto('http://evil.test/');
  const result = await evil.evaluate(async (base) => {
    const out: string[] = [];
    for (const init of [
      { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-Viper-UI': '1' }, body: '{"action":"up","machine":"web"}', credentials: 'include' as const },
      { method: 'POST', mode: 'no-cors' as const, headers: { 'Content-Type': 'text/plain' }, body: '{"action":"up","machine":"web"}', credentials: 'include' as const },
    ]) {
      try { const r = await fetch(base + '/api/run', init); out.push('status ' + r.status); } catch (e) { out.push('blocked'); }
    }
    return out;
  }, ui.base);
  expect(result.every((r) => r === 'blocked' || r === 'status 403')).toBeTruthy();
  const runs = await page.request.get(ui.base + '/api/runs');
  expect((await runs.json()).runs).toEqual([]); // nothing was started
});

test('the logs are cleaned and the secrets are hidden', async ({ page }) => {
  await page.goto(ui.start);
  await page.click('#tab-logs');
  const out = page.locator('#log-output');
  await expect(out).toContainText('e2e-web');
  await expect(out).not.toContainText('ghp_e2esecretvalue123');
  await expect(out).toContainText('[hidden]');
  await expect(out).not.toContainText('\u001b');
  await page.fill('#log-filter', 'nomatch');
  await expect(out).toHaveText('');
});

test('a preflight runs as viper itself and ends with its exit status', async ({ page }) => {
  await ui.stop();
  ui = await startUI([], 0); // a docker that answers at once, with nothing: the preflight ends with problems
  await page.goto(ui.start);
  await page.getByRole('button', { name: 'Preflight web', exact: true }).click();
  await expect(page.locator('#page-runs')).toBeVisible();
  await expect(page.locator('#run-output')).toContainText('$ viper preflight web');
  await expect(page.locator('#run-status')).toContainText('Ended: exit 1', { timeout: 40_000 });
  await expect(page.locator('#run-output')).toContainText('settings');
  await expect(page.locator('#run-output')).not.toContainText('ghp_e2esecretvalue123');
});

test('the start file that opens the browser leads to the page, and the key is not in the command line', async ({ page, browserName }) => {
  // Playwright's WebKit build does not follow any navigation from a file:// page to http:// (meta refresh, script or a click on a
  // link), also for a plain test page; not seen in a real Safari, and Jupyter uses the same start file. Not verified there.
  test.skip(browserName === 'webkit' && process.platform === 'linux', 'the WebKit build of Playwright for Linux does not leave a file:// page');
  test.skip(!canFakeOpener, 'rundll32 (the opener on Windows) cannot be faked');
  await ui.stop();
  ui = await startUI([], 30, true);
  await page.goto(ui.start); // file://...start.html: a refresh to http://127.0.0.1:port/?t=key, then the redirect
  if (browserName === 'webkit') {
    // Safari's engine may not follow a refresh from a file:// page (a navigation without a person): the link in the file is for that
    await page.waitForTimeout(3000);
    if (page.url().startsWith('file:')) { await page.getByRole('link', { name: 'viper ui' }).click(); }
  }
  await expect(page).toHaveURL(ui.base + '/');
  await expect(page.locator('#machines tbody tr')).toHaveCount(3);
  expect(ui.output()).not.toMatch(/\?t=/); // the terminal does not show the key either
});
