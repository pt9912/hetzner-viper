import { expect, test } from '@playwright/test';
import * as fs from 'node:fs';
import * as path from 'node:path';
import { startUI, UI } from '../fixture';

// the page of the settings with many files and a file with many settings
let ui: UI;
test.afterEach(async () => { await ui.stop(); });

test('many files and a long file: the files are closed blocks, a file loads when it opens, 200 settings at a time, and a search finds across files', async ({ page }) => {
  ui = await startUI([], 0);
  for (let i = 0; i < 80; i++) { fs.writeFileSync(path.join(ui.work, 'machines', `m${i}.env.local`), 'SERVER_TYPE=cpx22\nNOTE=x\n'); }
  const many = Array.from({ length: 450 }, (_, i) => `SETTING_${String(i).padStart(3, '0')}=v${i}`).join('\n') + '\n';
  fs.writeFileSync(path.join(ui.work, 'groups', 'long.env'), many);
  fs.writeFileSync(path.join(ui.work, 'machines', 'huge.env.local'), 'A=1\n'.repeat(400_000)); // more than a MiB
  await page.goto(ui.start);
  await page.click('#tab-settings');
  // .env.local is open, the others are closed: the page has the list, not the settings of 85 files
  await expect(page.locator('details.file').nth(80)).toBeAttached(); // the list is built (more than 80 files)
  expect(await page.locator('details.file').count()).toBeGreaterThan(80);
  await expect(page.locator('details.file[open]')).toHaveCount(1);
  await expect(page.locator('details.file[open] summary')).toContainText('.env.local');
  expect(await page.locator('#settings-files tbody tr').count()).toBeLessThan(40);
  // the file that is too large says so (and loads nothing)
  await expect(page.locator('details.file', { hasText: 'machines/huge.env.local' })).toContainText('larger than 1 MiB');
  // a long file: the first 200, then a button for more
  const long = page.locator('details.file', { hasText: 'groups/long.env' });
  await long.locator('summary').click();
  await expect(long.locator('tbody tr')).toHaveCount(200);
  await expect(long.locator('button.more')).toContainText('Show 200 more (250 left)');
  await long.locator('button.more').click();
  await expect(long.locator('tbody tr')).toHaveCount(400);
  await long.locator('button.more').click();
  await expect(long.locator('tbody tr')).toHaveCount(450);
  await expect(long.locator('button.more')).toHaveCount(0);
  // the search goes over the names of all files and says where it found
  await page.fill('#settings-q', 'server_type');
  await expect(page.locator('#settings-hits')).toContainText('hits');
  await expect(page.locator('#settings-hits tbody tr').first()).toContainText('SERVER_TYPE');
  await expect(page.locator('#settings-hits')).toContainText('machines/m7.env.local');
  await expect(page.locator('#settings-files')).toBeHidden();
  await page.fill('#settings-q', 'no_such_setting');
  await expect(page.locator('#settings-hits')).toContainText('0 hits');
  await page.fill('#settings-q', '');
  await expect(page.locator('#settings-files')).toBeVisible();
  await expect(page.locator('details.file[open]')).toHaveCount(2); // what was open stays open
});

test('the answers of the server stay small', async ({ page }) => {
  ui = await startUI([], 0);
  for (let i = 0; i < 300; i++) { fs.writeFileSync(path.join(ui.work, 'machines', `n${i}.env.local`), Array.from({ length: 20 }, (_, k) => `K${k}=v`).join('\n') + '\n'); }
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit').first()).toBeVisible(); // the exchange page has moved on
  const sizes = await page.evaluate(async () => {
    const get = async (u: string) => (await (await fetch(u, { credentials: 'same-origin' })).text()).length;
    return { list: await get('/api/settings'), one: await get('/api/settings?file=machines/n7.env.local'), hits: await get('/api/settings?q=K1') };
  });
  expect(sizes.list).toBeLessThan(60_000);
  expect(sizes.one).toBeLessThan(5_000);
  expect(sizes.hits).toBeLessThan(120_000);
});
