import AxeBuilder from '@axe-core/playwright';
import { expect, test } from '@playwright/test';
import * as fs from 'node:fs';
import * as path from 'node:path';
import { startUI, UI } from '../fixture';

// viper ui --edit: the credentials (docs/plan/ui-bearbeiten.md, stage C). A value goes in once and is shown nowhere.
let ui: UI;
test.afterEach(async () => { await ui.stop(); });

const TOKEN = 'hcloud-e2e-new-token-0123456789';
const base = () => path.join(ui.work, '.env.local');

test('without --edit there is no page for credentials', async ({ page }) => {
  ui = await startUI([], 0);
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit')).toHaveCount(3);
  await expect(page.locator('#tab-secrets')).toBeHidden();
});

test('a credential is set, replaced and removed, and its value is nowhere on the page, in the log or in the copy', async ({ page }) => {
  ui = await startUI(['--edit'], 0);
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit')).toHaveCount(3);
  await page.click('#tab-secrets');
  // the table says where a credential is set (names only), and the old value is not on the page
  await expect(page.locator('#s-table tbody tr', { hasText: 'VIPER_GH_TOKEN' })).toContainText('.env.local');
  await expect(page.locator('#s-table tbody tr', { hasText: 'HCLOUD_TOKEN' })).toContainText('not set');
  expect(await page.locator('body').innerText()).not.toContain('ghp_e2esecretvalue123');
  // too short: refused with the reason, nothing written
  await page.selectOption('#s-name', 'HCLOUD_TOKEN');
  await expect(page.locator('#s-file')).toHaveValue('.env.local');
  await page.fill('#s-value', 'short');
  await page.click('#s-preview');
  await expect(page.locator('#s-problems')).toContainText('at least 8');
  await expect(page.locator('#s-apply')).toBeDisabled();
  // set it
  await page.fill('#s-value', TOKEN);
  await page.click('#s-preview');
  await expect(page.locator('#s-text')).toContainText('+ HCLOUD_TOKEN: set');
  await expect(page.locator('#s-apply')).toBeEnabled();
  await page.fill('#s-value', TOKEN + 'x'); // the form changed: the preview is out of date
  await expect(page.locator('#s-apply')).toBeDisabled();
  await page.fill('#s-value', TOKEN);
  await page.click('#s-preview');
  await page.click('#s-apply');
  await expect(page.locator('#s-status')).toContainText('Done: HCLOUD_TOKEN');
  await expect(page.locator('#s-status')).toBeFocused();
  await expect(page.locator('#s-value')).toHaveValue('');
  expect(fs.readFileSync(base(), 'utf8')).toContain(`HCLOUD_TOKEN='${TOKEN}'`);
  expect(fs.readFileSync(base(), 'utf8')).toContain('VIPER_GH_TOKEN=ghp_e2esecretvalue123\n'); // the rest is as it was
  await expect(page.locator('#s-table tbody tr', { hasText: 'HCLOUD_TOKEN' })).toContainText('.env.local');
  expect(await page.locator('body').innerText()).not.toContain(TOKEN);
  expect(await page.content()).not.toContain(TOKEN);
  const backups = path.join(ui.home, '.local', 'state', 'viper', 'backups');
  const zips = fs.readdirSync(backups).filter((n) => n.endsWith('.zip'));
  expect(zips).toHaveLength(1);
  if (process.platform !== 'win32') { expect(fs.statSync(path.join(backups, zips[0])).mode & 0o777).toBe(0o600); }
  const log = fs.readFileSync(path.join(ui.home, '.local', 'state', 'viper', 'events.log'), 'utf8');
  expect(log).toMatch(/ui\s+edit\s+set HCLOUD_TOKEN in \.env\.local \(backup /);
  expect(log).not.toContain(TOKEN);
  // replaced
  await page.fill('#s-value', TOKEN + '-2');
  await page.click('#s-preview');
  await expect(page.locator('#s-text')).toContainText('~ HCLOUD_TOKEN: replaced');
  await page.click('#s-apply');
  await expect.poll(() => fs.readFileSync(base(), 'utf8')).toContain(`HCLOUD_TOKEN='${TOKEN}-2'`);
  // removed: the button asks again, with the names in the question
  await page.selectOption('#s-name', 'HCLOUD_TOKEN');
  await page.click('#s-remove');
  await expect(page.locator('#s-remove')).toContainText('Press again to remove HCLOUD_TOKEN from .env.local');
  expect(fs.readFileSync(base(), 'utf8')).toContain('HCLOUD_TOKEN'); // nothing yet
  await page.click('#s-remove');
  await expect.poll(() => fs.readFileSync(base(), 'utf8').includes('HCLOUD_TOKEN')).toBe(false);
  await expect(page.locator('#s-status')).toContainText('Done: HCLOUD_TOKEN');
  await expect(page.locator('#s-remove')).toHaveText('Remove from the file');
  expect(fs.readdirSync(backups).filter((n) => n.endsWith('.zip'))).toHaveLength(3);
});

test('the value is wiped when the person leaves the page, and a file that changed is not replaced', async ({ page }) => {
  ui = await startUI(['--edit'], 0);
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit')).toHaveCount(3);
  await page.click('#tab-secrets');
  await page.selectOption('#s-name', 'HCLOUD_TOKEN');
  await page.fill('#s-value', TOKEN);
  await page.click('#s-preview');
  await expect(page.locator('#s-apply')).toBeEnabled();
  fs.appendFileSync(base(), '# changed at the terminal\n');
  await page.click('#s-apply');
  await expect(page.locator('#s-problems')).toContainText('has changed since the preview');
  expect(fs.readFileSync(base(), 'utf8')).not.toContain('HCLOUD_TOKEN');
  await page.click('#tab-overview');
  await page.click('#tab-secrets');
  await expect(page.locator('#s-value')).toHaveValue('');
  await expect(page.locator('#s-apply')).toBeDisabled();
});

for (const scheme of ['light', 'dark'] as const) {
  for (const lang of ['en', 'de']) {
    test(`the page for credentials has no accessibility violations (${scheme}, ${lang})`, async ({ browser }) => {
      ui = await startUI(['--edit'], 0);
      const ctx = await browser.newContext({ colorScheme: scheme, locale: lang });
      const page = await ctx.newPage();
      await page.goto(ui.start);
      await expect(page.locator('#machines tr.unit')).toHaveCount(3);
      await page.click('#tab-secrets');
      await page.selectOption('#s-name', 'HCLOUD_TOKEN');
      await page.fill('#s-value', TOKEN);
      await page.click('#s-preview');
      await expect(page.locator('#s-result')).toBeVisible();
      const result = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'best-practice']).analyze();
      expect(result.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(' ')).join(', ')}`), `${scheme} ${lang}`).toEqual([]);
      await ctx.close();
    });
  }
}
