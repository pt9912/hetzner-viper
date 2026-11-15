import { expect, test } from '@playwright/test';
import * as fs from 'node:fs';
import * as path from 'node:path';
import { startUI, UI } from '../fixture';

let ui: UI;
test.beforeEach(async () => { ui = await startUI([], 0); });
test.afterEach(async () => { await ui.stop(); });

const names = (page: import('@playwright/test').Page) => page.locator('#machines tr.unit td:first-child').allInnerTexts();

test('the overview is filtered by text and by state, and the filter can be cleared', async ({ page }) => {
  // web is set up, a is started, b is not started (the marks of this computer)
  const marks = path.join(ui.home, '.local', 'state', 'viper', 'servers', 'e2e');
  fs.mkdirSync(marks, { recursive: true });
  fs.writeFileSync(path.join(marks, 'id-e2e-web'), '1\n'); fs.writeFileSync(path.join(marks, 'ready-e2e-web'), '1\n');
  fs.writeFileSync(path.join(marks, 'id-e2e-a'), '2\n');
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit')).toHaveCount(3);
  await expect(page.locator('#f-clear')).toBeHidden();
  // text: one machine; its group head is left out when no machine of it is shown, the other head stays
  await page.fill('#f-text', 'web');
  await expect(page.locator('#machines tr.unit')).toHaveCount(1);
  await expect(page.locator('#machines tr.grouphead.real')).toHaveCount(0);
  await expect(page.locator('#f-count')).toContainText('1 of 3 machines');
  // all the words must fit (name and provider)
  await page.fill('#f-text', 'web compose');
  await expect(page.locator('#machines tr.unit')).toHaveCount(1);
  await page.fill('#f-text', 'web hetzner');
  await expect(page.locator('#machines tr.unit')).toHaveCount(0);
  await expect(page.locator('#f-count')).toContainText('No machine matches the filter.');
  // the name of a group shows all its machines
  await page.fill('#f-text', 'dev');
  await expect(page.locator('#machines tr.unit')).toHaveCount(2);
  await expect(page.locator('#machines tr.grouphead.real')).toHaveCount(1);
  // the state; the state of the group head is still that of the whole group
  await page.fill('#f-text', '');
  await page.selectOption('#f-state', 'started');
  await expect(page.locator('#machines tr.unit')).toHaveCount(1);
  await expect(page.locator('#machines tr.grouphead.real')).toContainText('partly');
  await page.click('#f-clear');
  await expect(page.locator('#machines tr.unit')).toHaveCount(3);
  await expect(page.locator('#f-text')).toBeFocused();
  await expect(page.locator('#f-clear')).toBeHidden();
});

test('the columns sort the machines of each group; a second click turns it, a third takes it away', async ({ page }) => {
  const marks = path.join(ui.home, '.local', 'state', 'viper', 'servers', 'e2e');
  fs.mkdirSync(marks, { recursive: true });
  fs.writeFileSync(path.join(marks, 'id-e2e-a'), '2\n'); // a is started, b is not
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit')).toHaveCount(3);
  const inGroup = () => page.locator('#machines tbody.rack').first().locator('tr.unit td:first-child').allInnerTexts();
  expect(await inGroup()).toEqual(['a', 'b']); // as the files have them
  const name = page.locator('#machines thead th').first();
  await page.getByRole('button', { name: /^Machine/ }).click();
  await expect(name).toHaveAttribute('aria-sort', 'ascending');
  expect(await inGroup()).toEqual(['a', 'b']);
  await page.getByRole('button', { name: /^Machine/ }).click();
  await expect(name).toHaveAttribute('aria-sort', 'descending');
  expect(await inGroup()).toEqual(['b', 'a']);
  await page.getByRole('button', { name: /^Machine/ }).click();
  await expect(name).toHaveAttribute('aria-sort', 'none');
  // by state: set up, then started, then not started; the groups stay where they are
  await page.getByRole('button', { name: /^State/ }).click();
  expect(await inGroup()).toEqual(['a', 'b']); // started before not started
  await page.getByRole('button', { name: /^State/ }).click();
  expect(await inGroup()).toEqual(['b', 'a']);
  await expect(page.locator('#machines tr.grouphead').first()).toContainText('dev');
  // the sort works from the keyboard
  await page.getByRole('button', { name: /^Provider/ }).focus();
  await page.keyboard.press('Enter');
  await expect(page.locator('#machines thead th').nth(1)).toHaveAttribute('aria-sort', 'ascending');
});

test('filter and sort stay when the page is drawn again (a run ends)', async ({ page }) => {
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit')).toHaveCount(3);
  await page.fill('#f-text', 'compose');
  await page.getByRole('button', { name: /^Machine/ }).click();
  await page.getByRole('button', { name: 'Preflight web', exact: true }).click();
  await expect(page.locator('#run-status')).toContainText('Ended: exit 1', { timeout: 30_000 });
  await page.click('#tab-overview');
  await expect(page.locator('#f-text')).toHaveValue('compose');
  await expect(page.locator('#machines thead th').first()).toHaveAttribute('aria-sort', 'ascending');
});
