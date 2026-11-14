import AxeBuilder from '@axe-core/playwright';
import { expect, test } from '@playwright/test';
import { startUI, UI } from '../fixture';

let ui: UI;
test.beforeEach(async () => { ui = await startUI(); });
test.afterEach(async () => { await ui.stop(); });

const PAGES = ['overview', 'runs', 'nats', 'settings', 'logs'];

for (const scheme of ['light', 'dark'] as const) {
  for (const lang of ['en', 'de']) {
    test(`no accessibility violations on any page (${scheme}, ${lang})`, async ({ browser }) => {
      const ctx = await browser.newContext({ colorScheme: scheme, locale: lang });
      const page = await ctx.newPage();
      await page.goto(ui.start);
      await expect(page.locator('#machines tbody tr')).toHaveCount(3);
      for (const p of PAGES) {
        await page.click('#tab-' + p);
        await expect(page.locator('#page-' + p)).toBeVisible();
        if (p === 'settings') { await expect(page.locator('#settings-files table').first()).toBeVisible(); }
        const result = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'best-practice']).analyze();
        expect(result.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(' ')).join(', ')}`), `${p} ${scheme} ${lang}`).toEqual([]);
      }
      await ctx.close();
    });
  }
}

test('the page can be used with the keyboard: skip link, tabs with the arrow keys, buttons', async ({ page }) => {
  await page.goto(ui.start);
  await expect(page.locator('#machines tbody tr')).toHaveCount(3);
  await page.keyboard.press('Tab');
  await expect(page.locator('.skip')).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(/#main$/);
  await page.focus('#tab-overview');
  await page.keyboard.press('ArrowRight');
  await expect(page.locator('#tab-runs')).toBeFocused();
  await expect(page.locator('#tab-runs')).toHaveAttribute('aria-selected', 'true');
  await expect(page.locator('#page-runs')).toBeVisible();
  await page.keyboard.press('End');
  await expect(page.locator('#tab-logs')).toBeFocused();
  await expect(page.locator('#page-logs')).toBeVisible();
  await page.keyboard.press('ArrowRight'); // from the last to the first
  await expect(page.locator('#tab-overview')).toBeFocused();
  // only the selected tab is in the order of the Tab key
  await expect(page.locator('#tab-runs')).toHaveAttribute('tabindex', '-1');
  // a button works with Enter
  await page.getByRole('button', { name: 'Log of web', exact: true }).focus();
  await page.keyboard.press('Enter');
  await expect(page.locator('#page-logs')).toBeVisible();
  await expect(page.locator('#log-output')).toContainText('e2e-web');
});

test('German: the page speaks German, keeps the choice, and the names for screen readers are German too', async ({ page }) => {
  await page.goto(ui.start);
  await expect(page.locator('html')).toHaveAttribute('lang', 'en');
  await page.selectOption('#lang', 'de');
  await expect(page.locator('html')).toHaveAttribute('lang', 'de');
  await expect(page.locator('#tab-settings')).toHaveText('Einstellungen');
  await expect(page.locator('#h-overview')).toHaveText('Rechner');
  await expect(page.getByRole('button', { name: 'web prüfen', exact: true })).toBeVisible();
  await expect(page.locator('#machines tbody tr', { hasText: 'web' })).toContainText('nicht gestartet');
  await page.reload();
  await expect(page.locator('html')).toHaveAttribute('lang', 'de');
  await expect(page.locator('#tab-logs')).toHaveText('Protokolle');
  await page.selectOption('#lang', 'en');
  await expect(page.locator('#tab-logs')).toHaveText('Logs');
});

test('the browser language decides when nothing was chosen', async ({ browser }) => {
  const ctx = await browser.newContext({ locale: 'de-DE' });
  const page = await ctx.newPage();
  await page.goto(ui.start);
  await expect(page.locator('#tab-overview')).toHaveText('Übersicht');
  await expect(page.locator('#lang')).toHaveValue('de');
  await ctx.close();
});

test('settings: names of every file, values only where it is safe, never a credential', async ({ page }) => {
  await page.goto(ui.start);
  await page.click('#tab-settings');
  const box = page.locator('#settings-files');
  await expect(box).toContainText('.env.local');
  await expect(box).toContainText('groups/dev.env');
  await expect(box).toContainText('machines/a.env.local');
  const row = (name: string) => box.locator('tr', { has: page.locator('td', { hasText: new RegExp(`^${name}$`) }) });
  await expect(row('PROVIDER')).toContainText('compose');
  await expect(row('GROUP_MACHINES')).toContainText('a,b');
  await expect(row('VIPER_GH_TOKEN')).toContainText('set');
  await expect(row('VIPER_GH_TOKEN')).not.toContainText('ghp_');
  expect(await box.innerText()).not.toContain('ghp_e2esecretvalue123');
  await page.selectOption('#lang', 'de');
  await expect(row('VIPER_GH_TOKEN')).toContainText('gesetzt');
});

test('a refused page and a missing page tell the person what to do', async ({ browser, page }) => {
  const ctx = await browser.newContext();
  const bare = await ctx.newPage();
  const res = await bare.goto(ui.base + '/');
  expect(res?.status()).toBe(403);
  await expect(bare.locator('h1')).toContainText('Not allowed');
  await expect(bare.locator('body')).toContainText('address that viper printed');
  await expect(bare.locator('body')).toContainText('Adresse, die viper');
  await ctx.close();
  await page.goto(ui.start);
  const missing = await page.goto(ui.base + '/nothing-here');
  expect(missing?.status()).toBe(404);
  await expect(page.locator('h1')).toContainText('Not found');
});

test('when the session is gone the page says so', async ({ page }) => {
  await page.goto(ui.start);
  await expect(page.locator('#machines tbody tr')).toHaveCount(3);
  await page.route('**/api/overview', (r) => r.fulfill({ status: 403, contentType: 'text/plain', body: 'forbidden' }));
  await page.click('#tab-logs');
  await page.click('#log-reload');
  await page.route('**/api/log**', (r) => r.fulfill({ status: 403, contentType: 'text/plain', body: 'forbidden' }));
  await page.click('#log-reload');
  await expect(page.locator('#banner')).toBeVisible();
  await expect(page.locator('#banner')).toContainText('session has ended');
  await expect(page.locator('#banner')).toHaveAttribute('role', 'alert');
});

test('the pages are small and need nothing from elsewhere', async ({ page }) => {
  const sizes: Record<string, number> = {};
  const hosts = new Set<string>();
  page.on('response', async (r) => { hosts.add(new URL(r.url()).host); sizes[new URL(r.url()).pathname] = (await r.body().catch(() => Buffer.alloc(0))).length; });
  await page.goto(ui.start);
  await expect(page.locator('#machines tbody tr')).toHaveCount(3);
  expect([...hosts]).toEqual([new URL(ui.base).host]);
  const total = Object.values(sizes).reduce((a, b) => a + b, 0);
  expect(total).toBeLessThan(80 * 1024);
});
