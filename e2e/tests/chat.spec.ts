import AxeBuilder from '@axe-core/playwright';
import { expect, test } from '@playwright/test';
import { startUI, UI } from '../fixture';

// viper ui-chat: the first stage (docs/plan/ui-chat.md): the page, the groups, the state of the operator, one at a time
let ui: UI;
test.afterEach(async () => { await ui.stop(); });

test('without the key of the operator the chat says what to do, and offers no conversation', async ({ page }) => {
  ui = await startUI([], 0, false, { command: 'ui-chat' });
  await page.goto(ui.start);
  await expect(page.locator('#me')).toContainText('not logged in');
  await expect(page.locator('#empty')).toContainText('You are not logged in in this group');
  await expect(page.locator('#empty code')).toHaveText('viper nats init dev');
  await expect(page.locator('.conv-btn')).toHaveCount(0);
});

test('with the key the machines of the group are conversations, and one opens', async ({ page }) => {
  ui = await startUI([], 0, false, { command: 'ui-chat', operator: true });
  await page.goto(ui.start);
  await expect(page.locator('#me')).toContainText('Operator operator logged in');
  await expect(page.locator('.group h2')).toHaveText('dev');
  await expect(page.locator('.conv-btn')).toHaveCount(2);
  await page.getByRole('button', { name: 'a', exact: true }).click();
  await expect(page.locator('#conv-name')).toHaveText('a');
  await expect(page.locator('#conv-sub')).toContainText('Machine of the group dev');
  await expect(page.getByRole('button', { name: 'a', exact: true })).toHaveAttribute('aria-current', 'true');
  await expect(page.locator('#msgs')).toHaveAttribute('role', 'log');
});

test('the language can be changed and the texts follow', async ({ page }) => {
  ui = await startUI([], 0, false, { command: 'ui-chat' });
  await page.goto(ui.start);
  await page.selectOption('#lang', 'de');
  await expect(page.locator('#me')).toContainText('nicht angemeldet');
  await expect(page.locator('#empty h2')).toHaveText('Sie sind in dieser Gruppe nicht angemeldet');
});

test('the chat has no route of viper ui: it cannot start a run', async ({ page }) => {
  ui = await startUI([], 0, false, { command: 'ui-chat', operator: true });
  await page.goto(ui.start);
  await expect(page.locator('#me')).toContainText('logged in'); // the start page has moved on to the chat
  const codes = await page.evaluate(async () => {
    const out: number[] = [];
    for (const p of ['/api/run', '/api/check', '/api/edit/apply']) {
      const r = await fetch(p, { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json', 'X-Viper-UI': '1' }, body: '{"action":"down","machine":"web"}' });
      out.push(r.status);
    }
    out.push((await fetch('/api/overview', { credentials: 'same-origin' })).status);
    return out;
  });
  expect(codes.every((c) => c >= 400)).toBe(true);
});

test('a second viper ui-chat says that one runs already, and with which port', async () => {
  ui = await startUI([], 0, false, { command: 'ui-chat' });
  const { spawnSync } = await import('node:child_process');
  const { envWith } = await import('../fixture');
  const r = spawnSync(process.env.VIPER_BIN as string, ['ui-chat', '--no-open'], { cwd: ui.work, env: envWith({ HOME: ui.home, USERPROFILE: ui.home }, ''), encoding: 'utf8', timeout: 20_000 });
  expect(r.status).not.toBe(0);
  expect(r.stderr).toMatch(/it runs already \(port \d+\)/);
});

for (const scheme of ['light', 'dark'] as const) {
  for (const lang of ['en', 'de']) {
    test(`the chat has no accessibility violations (${scheme}, ${lang})`, async ({ browser }) => {
      ui = await startUI([], 0, false, { command: 'ui-chat', operator: true });
      const ctx = await browser.newContext({ colorScheme: scheme, locale: lang });
      const page = await ctx.newPage();
      await page.goto(ui.start);
      await expect(page.locator('.conv-btn')).toHaveCount(2);
      await page.locator('.conv-btn').first().click();
      const result = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'best-practice']).analyze();
      expect(result.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(' ')).join(', ')}`), `${scheme} ${lang}`).toEqual([]);
      await ctx.close();
    });
  }
}
