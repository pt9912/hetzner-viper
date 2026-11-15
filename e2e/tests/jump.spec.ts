import AxeBuilder from '@axe-core/playwright';
import { expect, test } from '@playwright/test';
import * as fs from 'node:fs';
import * as path from 'node:path';
import { startUI, UI } from '../fixture';

// the name of a machine or group in the overview leads to its settings
let ui: UI;
test.afterEach(async () => { await ui.stop(); });

test('without --edit the name of a machine leads to the file in the settings page, with the focus on it', async ({ page }) => {
  ui = await startUI([], 0);
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit')).toHaveCount(3);
  await page.getByRole('button', { name: 'Settings of web', exact: true }).click();
  await expect(page.locator('#page-settings')).toBeVisible();
  await expect(page.locator('summary.file-title', { hasText: 'machines/web.env.local' })).toBeFocused();
  await expect(page.locator('#settings-note')).toHaveText('');
  // a group leads to its file
  await page.click('#tab-overview');
  await page.getByRole('button', { name: 'Settings of group dev', exact: true }).click();
  await expect(page.locator('summary.file-title', { hasText: 'groups/dev.env' })).toBeFocused();
});

test('a machine that only a group names leads to the group file, and says so', async ({ page }) => {
  ui = await startUI([], 0);
  fs.rmSync(path.join(ui.work, 'machines', 'a.env.local'));
  fs.writeFileSync(path.join(ui.work, 'groups', 'dev.env'), 'GROUP_MACHINES=a,b\nNATS_OBJECTS=true\n');
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit').first()).toBeVisible();
  await page.getByRole('button', { name: 'Settings of a', exact: true }).first().click();
  await expect(page.locator('summary.file-title', { hasText: 'groups/dev.env' })).toBeFocused();
  await expect(page.locator('#settings-note')).toContainText('has no file of its own');
});

test('with --edit the name leads to the page to change, with the file chosen', async ({ page }) => {
  ui = await startUI(['--edit'], 0);
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit')).toHaveCount(3);
  await page.getByRole('button', { name: 'Settings of web', exact: true }).click();
  await expect(page.locator('#page-change')).toBeVisible();
  await expect(page.locator('#ch-file')).toHaveValue('machines/web.env.local');
  await expect(page.locator('#ch-file')).toBeFocused();
  await page.click('#tab-overview');
  await page.getByRole('button', { name: 'Settings of group dev', exact: true }).click();
  await expect(page.locator('#ch-file')).toHaveValue('groups/dev.env');
  await expect(page.locator('#ch-table tbody tr', { hasText: 'GROUP_MACHINES' })).toBeVisible();
});

test('the names stay readable text for the filter and the sort, and have no accessibility violation', async ({ page }) => {
  ui = await startUI(['--edit'], 0);
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit')).toHaveCount(3);
  await page.fill('#f-text', 'web');
  await expect(page.locator('#machines tr.unit')).toHaveCount(1);
  expect(await page.locator('#machines tr.unit td:first-child').innerText()).toBe('web');
  await page.fill('#f-text', '');
  const result = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'best-practice']).analyze();
  expect(result.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(' ')).join(', ')}`)).toEqual([]);
});
