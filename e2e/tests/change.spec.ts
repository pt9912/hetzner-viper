import AxeBuilder from '@axe-core/playwright';
import { expect, test } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import * as fs from 'node:fs';
import * as path from 'node:path';
import { startUI, UI } from '../fixture';

// viper ui --edit: change the settings of a file that exists (docs/plan/ui-bearbeiten.md, stage B).
let ui: UI;
test.afterEach(async () => { await ui.stop(); });
const web = () => path.join(ui.work, 'machines', 'web.env.local');

async function open(page: import('@playwright/test').Page) {
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit')).toHaveCount(3);
  await page.click('#tab-change');
  await page.selectOption('#ch-file', 'machines/web.env.local');
}

test('without --edit there is no page to change settings', async ({ page }) => {
  ui = await startUI([], 0);
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit')).toHaveCount(3);
  await expect(page.locator('#tab-change')).toBeHidden();
});

test('a setting is added, changed and taken out; every change keeps a copy that can be downloaded', async ({ page }) => {
  ui = await startUI(['--edit'], 0);
  await open(page);
  await page.selectOption('#ch-name', 'SERVER_TYPE');
  await expect(page.locator('#ch-help')).toContainText('Size of the machine');
  await page.fill('#ch-value', '2x4');
  await expect(page.locator('#ch-apply')).toBeDisabled();
  await page.click('#ch-preview');
  await expect(page.locator('#ch-text')).toContainText('+ SERVER_TYPE = 2x4');
  await page.click('#ch-apply');
  await expect(page.locator('#ch-status')).toContainText('Done: SERVER_TYPE');
  await expect(page.locator('#ch-status')).toBeFocused();
  expect(fs.readFileSync(web(), 'utf8')).toBe("SERVER_TYPE='2x4'\n");
  await expect(page.locator('#ch-table tbody tr', { hasText: 'SERVER_TYPE' })).toContainText('2x4');
  // changed: the old and the new value are in the preview
  await page.fill('#ch-value', '4x8');
  await page.click('#ch-preview');
  await expect(page.locator('#ch-text')).toContainText('~ SERVER_TYPE: 2x4 -> 4x8');
  await page.click('#ch-apply');
  await expect.poll(() => fs.readFileSync(web(), 'utf8')).toBe("SERVER_TYPE='4x8'\n");
  // the copies: listed, and a download is a ZIP
  await expect(page.locator('#backup-list li')).toHaveCount(2);
  const link = page.locator('#backup-list a').first();
  const href = await link.getAttribute('href');
  const res = await page.evaluate(async (h) => { const r = await fetch(h!, { credentials: 'same-origin' }); return { type: r.headers.get('content-type'), disp: r.headers.get('content-disposition'), head: new TextDecoder().decode((await r.arrayBuffer()).slice(0, 2)) }; }, href);
  expect(res.type).toBe('application/octet-stream');
  expect(res.disp).toContain('attachment');
  expect(res.head).toBe('PK');
  // taken out: the button asks again, with the name in the question
  const row = page.locator('#ch-table tbody tr', { hasText: 'SERVER_TYPE' });
  await row.getByRole('button', { name: 'Remove SERVER_TYPE' }).click();
  await expect(page.getByRole('button', { name: 'Press again to remove SERVER_TYPE' })).toBeVisible();
  expect(fs.readFileSync(web(), 'utf8')).toContain('SERVER_TYPE');
  await page.getByRole('button', { name: 'Press again to remove SERVER_TYPE' }).click();
  await expect.poll(() => fs.readFileSync(web(), 'utf8')).toBe('');
  const log = fs.readFileSync(path.join(ui.home, '.local', 'state', 'viper', 'events.log'), 'utf8');
  expect(log).toMatch(/ui\s+edit\s+changed SERVER_TYPE in machines\/web\.env\.local \(backup /);
});

test('an earlier version is put back: the preview says what changes, the credentials that are there stay, and the restore keeps a copy', async ({ page }) => {
  ui = await startUI(['--edit'], 0);
  await open(page);
  await page.selectOption('#ch-name', 'SERVER_TYPE');
  for (const v of ['2x4', '4x8']) {
    await page.fill('#ch-value', v);
    await page.click('#ch-preview');
    await page.click('#ch-apply');
    await expect.poll(() => fs.readFileSync(web(), 'utf8')).toBe(`SERVER_TYPE='${v}'\n`);
  }
  await expect(page.locator('#backup-list li')).toHaveCount(2);
  await page.locator('#backup-list li').first().getByRole('button', { name: /^Restore the copy of/ }).click();
  await expect(page.locator('#ch-text')).toContainText('~ SERVER_TYPE: 4x8 -> 2x4');
  await expect(page.locator('#ch-restore-go')).toBeVisible();
  expect(fs.readFileSync(web(), 'utf8')).toBe("SERVER_TYPE='4x8'\n"); // the preview wrote nothing
  await page.click('#ch-restore-go');
  await expect.poll(() => fs.readFileSync(web(), 'utf8')).toContain("2x4");
  await expect(page.locator('#backup-list li')).toHaveCount(3); // the restore kept a copy of what it replaced
  await expect(page.locator('#ch-restore-go')).toBeHidden();
  const log = fs.readFileSync(path.join(ui.home, '.local', 'state', 'viper', 'events.log'), 'utf8');
  expect(log).toMatch(/ui\s+edit\s+restored machines\/web\.env\.local from \d{8}T\d{6}-setting(-\d+)?\.zip \(backup /);
});

test('a dangerous setting is taken out only after its word is typed', async ({ page }) => {
  ui = await startUI(['--edit'], 0);
  fs.writeFileSync(web(), "COMPOSE_DOCKER='none'\n");
  await open(page);
  await page.locator('#ch-table tbody tr', { hasText: 'COMPOSE_DOCKER' }).getByRole('button', { name: 'Remove COMPOSE_DOCKER' }).click();
  await page.getByRole('button', { name: 'Press again to remove COMPOSE_DOCKER' }).click();
  await expect(page.locator('#ch-confirmrow')).toBeVisible();
  await expect(page.locator('#ch-restore-go')).toHaveText('Remove the setting');
  await page.click('#ch-restore-go');
  await expect(page.locator('#ch-status')).toContainText('Type web to confirm');
  expect(fs.readFileSync(web(), 'utf8')).toContain('COMPOSE_DOCKER');
  await page.fill('#ch-confirm', 'web');
  await page.click('#ch-restore-go');
  await expect.poll(() => fs.readFileSync(web(), 'utf8')).toBe('');
});

test('commands over several lines are written, read back into the form, and taken out only with the name', async ({ page }) => {
  ui = await startUI(['--edit'], 0);
  await open(page);
  await page.selectOption('#ch-name', 'VIPER_POST_CLONE');
  await expect(page.locator('textarea#ch-value')).toBeVisible();
  await expect(page.locator('#ch-help')).toContainText('Shell commands that run once');
  const cmd = "npm ci\nexport X='it'\\''s'\nmake \"$HOME\"";
  await page.fill('#ch-value', cmd);
  await page.click('#ch-preview');
  await expect(page.locator('#ch-text')).toContainText('+ VIPER_POST_CLONE = npm ci');
  await expect(page.locator('#ch-problems')).toContainText('shell commands that run on the server');
  await page.click('#ch-apply');
  await expect(page.locator('#ch-status')).toContainText('Type web to confirm');
  expect(fs.readFileSync(web(), 'utf8')).toBe('');
  await page.fill('#ch-confirm', 'web');
  await page.click('#ch-apply');
  await expect.poll(() => fs.readFileSync(web(), 'utf8')).toContain('VIPER_POST_CLONE=');
  // what the file holds is what bash reads
  if (process.platform !== 'win32') {
    expect(execFileSync('bash', ['-c', 'source "$1"; printf %s "$VIPER_POST_CLONE"', '_', web()]).toString()).toBe(cmd);
  }
  // the row's Change button reads the commands into the field
  await page.locator('#ch-table tbody tr', { hasText: 'VIPER_POST_CLONE' }).getByRole('button', { name: 'Change VIPER_POST_CLONE' }).click();
  await expect(page.locator('#ch-value')).toHaveValue(cmd);
  // taken out: the name again
  await page.locator('#ch-table tbody tr', { hasText: 'VIPER_POST_CLONE' }).getByRole('button', { name: 'Remove VIPER_POST_CLONE' }).click();
  await page.getByRole('button', { name: 'Press again to remove VIPER_POST_CLONE' }).click();
  await expect(page.locator('#ch-restore-go')).toHaveText('Remove the setting');
  await page.fill('#ch-confirm', 'web');
  await page.click('#ch-restore-go');
  await expect.poll(() => fs.readFileSync(web(), 'utf8')).toBe('');
});

test('a file changed at the terminal shows up by itself, and a preview that waits is out of date', async ({ page }) => {
  test.setTimeout(90_000);
  ui = await startUI(['--edit'], 0);
  await open(page);
  // the overview learns of a new machine without a reload
  await page.click('#tab-overview');
  await expect(page.locator('#machines tr.unit')).toHaveCount(3);
  fs.writeFileSync(path.join(ui.work, 'machines', 'late.env.local'), 'PROVIDER=compose\nSERVER_LOCATION=default\nCOMPOSE_IMAGE=x:1\n');
  await expect(page.locator('#machines tr.unit')).toHaveCount(4, { timeout: 20_000 });
  // a preview waits for "apply"; the file changes under it
  await page.click('#tab-change');
  await page.selectOption('#ch-file', 'machines/web.env.local');
  await page.selectOption('#ch-name', 'SERVER_TYPE');
  await page.fill('#ch-value', '2x4');
  await page.click('#ch-preview');
  await expect(page.locator('#ch-apply')).toBeEnabled();
  fs.appendFileSync(web(), '# changed at the terminal\n');
  await expect(page.locator('#ch-status')).toContainText('The files changed since the preview', { timeout: 20_000 });
  await expect(page.locator('#ch-apply')).toBeDisabled();
  // and the page offers the new file where the files are listed
  await expect(page.locator('#ch-file option', { hasText: 'machines/late.env.local' })).toHaveCount(1);
  expect(fs.readFileSync(web(), 'utf8')).not.toContain('SERVER_TYPE');
});

test('a preview is not wiped by the changes of the person themself or by the marks of a run, only by a settings file', async ({ page }) => {
  test.setTimeout(90_000);
  ui = await startUI(['--edit'], 0);
  await open(page);
  await page.selectOption('#ch-name', 'SERVER_TYPE');
  await page.fill('#ch-value', '2x4');
  await page.click('#ch-preview');
  await page.click('#ch-apply');
  await expect.poll(() => fs.readFileSync(web(), 'utf8')).toBe("SERVER_TYPE='2x4'\n");
  // a new preview right after the own change; the poll that comes does not take the own change for a foreign one
  await page.fill('#ch-value', '4x8');
  await page.click('#ch-preview');
  await expect(page.locator('#ch-apply')).toBeEnabled();
  await page.waitForTimeout(8_000); // at least one poll
  await expect(page.locator('#ch-apply')).toBeEnabled();
  // a mark of a run (a server) is no change of a settings file
  const marks = path.join(ui.home, '.local', 'state', 'viper', 'servers', 'e2e');
  fs.mkdirSync(marks, { recursive: true });
  fs.writeFileSync(path.join(marks, 'id-e2e-a'), '1\n');
  await page.waitForTimeout(8_000);
  await expect(page.locator('#ch-apply')).toBeEnabled();
  await page.click('#ch-apply');
  await expect.poll(() => fs.readFileSync(web(), 'utf8')).toBe("SERVER_TYPE='4x8'\n");
});

test('the help of a setting is in the language of the page', async ({ browser }) => {
  ui = await startUI(['--edit'], 0);
  const ctx = await browser.newContext({ locale: 'de' });
  const page = await ctx.newPage();
  await open(page);
  await page.selectOption('#ch-name', 'NATS_OBJECTS_TTL');
  await expect(page.locator('#ch-help')).toContainText('Wie lange eine Datei bleibt');
  await expect(page.locator('#ch-limits')).toContainText('Eine Zahl von 1 bis 365');
  await ctx.close();
});

test('what is refused says why; a dangerous setting asks for a word; a change that breaks others is not made', async ({ page }) => {
  ui = await startUI(['--edit'], 0);
  await open(page);
  // out of range
  await page.selectOption('#ch-name', 'NATS_PORT');
  await page.fill('#ch-value', '70000');
  await page.click('#ch-preview');
  await expect(page.locator('#ch-problems')).toContainText('a number from 1024 to 65535');
  await expect(page.locator('#ch-apply')).toBeDisabled();
  // dangerous: the word to type
  await page.selectOption('#ch-name', 'COMPOSE_DOCKER');
  await page.selectOption('#ch-value', 'dind');
  await page.click('#ch-preview');
  await expect(page.locator('#ch-problems')).toContainText('privileged');
  await expect(page.locator('#ch-confirmrow')).toBeVisible();
  await page.click('#ch-apply');
  await expect(page.locator('#ch-status')).toContainText('Type web to confirm');
  expect(fs.readFileSync(web(), 'utf8')).toBe('');
  await page.fill('#ch-confirm', 'web');
  await page.click('#ch-apply');
  await expect.poll(() => fs.readFileSync(web(), 'utf8')).toContain("COMPOSE_DOCKER='dind'");
  // the base file: cloudflare DNS needs a domain, which the machines lack
  await page.selectOption('#ch-file', '.env.local');
  await page.selectOption('#ch-name', 'DNS_PROVIDER');
  await page.selectOption('#ch-value', 'cloudflare');
  await page.click('#ch-preview');
  await expect(page.locator('#ch-problems')).toContainText('Would break');
  await expect(page.locator('#ch-apply')).toBeDisabled();
  expect(fs.readFileSync(path.join(ui.work, '.env.local'), 'utf8')).toContain('DNS_PROVIDER=none');
});

for (const scheme of ['light', 'dark'] as const) {
  for (const lang of ['en', 'de']) {
    test(`the page to change settings has no accessibility violations (${scheme}, ${lang})`, async ({ browser }) => {
      ui = await startUI(['--edit'], 0);
      const ctx = await browser.newContext({ colorScheme: scheme, locale: lang });
      const page = await ctx.newPage();
      await open(page);
      await page.selectOption('#ch-name', 'VIPER_POST_CLONE'); // the field over several lines, with the confirmation
      await page.fill('#ch-value', 'npm ci\nmake');
      await page.click('#ch-preview');
      await expect(page.locator('#ch-result')).toBeVisible();
      await expect(page.locator('#ch-confirmrow')).toBeVisible();
      const result = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'best-practice']).analyze();
      expect(result.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(' ')).join(', ')}`), `${scheme} ${lang}`).toEqual([]);
      await ctx.close();
    });
  }
}
