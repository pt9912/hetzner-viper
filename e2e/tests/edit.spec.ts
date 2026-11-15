import AxeBuilder from '@axe-core/playwright';
import { expect, test } from '@playwright/test';
import * as fs from 'node:fs';
import * as path from 'node:path';
import { startUI, UI } from '../fixture';

// viper ui --edit: new settings files (docs/plan/ui-bearbeiten.md, stage A). Without it the page offers none.
let ui: UI;
test.afterEach(async () => { await ui.stop(); });

test('without --edit there is no page to create and the server refuses', async ({ page }) => {
  ui = await startUI([], 0);
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit')).toHaveCount(3);
  await expect(page.locator('#tab-create')).toBeHidden();
  await expect(page.locator('#version')).not.toContainText('edit on');
  const code = await page.evaluate(async () => (await fetch('/api/edit/apply', {
    method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json', 'X-Viper-UI': '1' }, body: '{"kind":"machine","name":"x"}',
  })).status);
  expect(code).toBe(403);
});

test('a machine is made from the form: preview first, then the file, never over an existing one', async ({ page }) => {
  ui = await startUI(['--edit'], 0);
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit')).toHaveCount(3);
  await expect(page.locator('#version')).toContainText('edit on');
  await page.click('#tab-create');
  await expect(page.locator('#c-provider')).toHaveValue('hetzner');
  await page.selectOption('#c-provider', 'compose');
  await page.fill('#c-name', 'box');
  await page.fill('#c-type', '2x4');
  await expect(page.locator('#c-apply')).toBeDisabled(); // no preview yet
  await page.click('#c-preview');
  await expect(page.locator('#c-text')).toContainText("# machines/box.env.local");
  await expect(page.locator('#c-text')).toContainText("SERVER_TYPE='2x4'");
  await expect(page.locator('#c-text')).toContainText("DNS_PROVIDER='none'");
  await expect(page.locator('#c-apply')).toBeEnabled();
  await expect(fs.existsSync(path.join(ui.work, 'machines', 'box.env.local'))).toBe(false); // the preview wrote nothing
  await page.fill('#c-type', '4x8'); // the form changed: the preview is out of date
  await expect(page.locator('#c-apply')).toBeDisabled();
  await page.click('#c-preview');
  await page.click('#c-apply');
  await expect(page.locator('#c-status')).toContainText('Created machines/box.env.local');
  await expect(page.locator('#c-status')).toBeFocused();
  const file = path.join(ui.work, 'machines', 'box.env.local');
  expect(fs.readFileSync(file, 'utf8')).toContain("SERVER_TYPE='4x8'");
  if (process.platform !== 'win32') { expect(fs.statSync(file).mode & 0o777).toBe(0o600); }
  expect(fs.readFileSync(path.join(ui.home, '.local', 'state', 'viper', 'events.log'), 'utf8')).toMatch(/ui\s+edit\s+made machines\/box.env.local\n/);
  await page.click('#tab-overview');
  await expect(page.locator('#machines tr.unit')).toHaveCount(4);
  // the same name again: refused with the reason, nothing is replaced
  await page.click('#tab-create');
  await page.fill('#c-name', 'box');
  await page.click('#c-preview');
  await expect(page.locator('#c-problems')).toContainText('exists already');
  await expect(page.locator('#c-apply')).toBeDisabled();
  // a name a shell or a path cannot hold
  await page.fill('#c-name', '../x');
  await page.click('#c-preview');
  await expect(page.locator('#c-problems')).toContainText('lowercase');
});

test('a group names machines that exist, and one that would break another is refused', async ({ page }) => {
  ui = await startUI(['--edit'], 0);
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit')).toHaveCount(3);
  await page.click('#tab-create');
  await page.selectOption('#c-kind', 'group');
  await expect(page.locator('#c-members input')).toHaveCount(3);
  await page.fill('#c-name', 'ops');
  await page.check('#c-members input[value="a"]'); // a is in dev already: two groups would make it ambiguous
  await page.click('#c-preview');
  await expect(page.locator('#c-problems')).toContainText('Would break: machine a');
  await expect(page.locator('#c-apply')).toBeDisabled();
  await page.uncheck('#c-members input[value="a"]');
  await page.check('#c-members input[value="web"]');
  await page.click('#c-preview');
  await expect(page.locator('#c-apply')).toBeEnabled();
  await page.click('#c-apply');
  await expect(page.locator('#c-status')).toContainText('Created groups/ops.env');
  expect(fs.readFileSync(path.join(ui.work, 'groups', 'ops.env'), 'utf8')).toContain("GROUP_MACHINES='web'");
  await page.click('#tab-overview');
  await expect(page.locator('#machines tr.grouphead.real')).toHaveCount(2);
  // the base file exists: it is not offered
  await page.click('#tab-create');
  await expect(page.locator('#c-kind option[value="base"]')).toBeDisabled();
});

test('a machine and a group are duplicated: the form is filled, a credential is not taken over', async ({ page }) => {
  ui = await startUI(['--edit'], 0);
  fs.writeFileSync(path.join(ui.work, 'machines', 'src.env.local'), "PROVIDER=compose\nSERVER_TYPE=2x4\nSERVER_LOCATION=default\nHCLOUD_TOKEN=tok-e2e-0123456789\nREPO_URL=https://github.com/x/y.git\n");
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit')).toHaveCount(4);
  await page.getByRole('button', { name: 'Duplicate src', exact: true }).click();
  await expect(page.locator('#page-create')).toBeVisible();
  await expect(page.locator('#c-name')).toBeFocused();
  await expect(page.locator('#c-name')).toHaveValue('src-2');
  await expect(page.locator('#c-type')).toHaveValue('2x4');
  await expect(page.locator('#c-provider')).toHaveValue('compose');
  await expect(page.locator('#c-status')).toContainText('Copied from src. Not copied: HCLOUD_TOKEN, REPO_URL.');
  expect(await page.locator('body').innerText()).not.toContain('tok-e2e-0123456789');
  await page.click('#c-preview');
  await page.click('#c-apply');
  await expect(page.locator('#c-status')).toContainText('Created machines/src-2.env.local');
  const copy = fs.readFileSync(path.join(ui.work, 'machines', 'src-2.env.local'), 'utf8');
  expect(copy).toContain("SERVER_TYPE='2x4'");
  expect(copy).not.toContain('tok-e2e');
  // a group: its machines are not taken over (a machine in two groups breaks), so the person chooses
  await page.click('#tab-overview');
  await page.getByRole('button', { name: 'Duplicate group dev', exact: true }).click();
  await expect(page.locator('#c-kind')).toHaveValue('group');
  await expect(page.locator('#c-name')).toHaveValue('dev-2');
  await expect(page.locator('#c-members input:checked')).toHaveCount(0);
  await page.click('#c-preview');
  await expect(page.locator('#c-problems')).toContainText('at least one');
  await page.check('#c-members input[value="web"]');
  await page.click('#c-preview');
  await page.click('#c-apply');
  await expect(page.locator('#c-status')).toContainText('Created groups/dev-2.env');
});

test('without --edit the overview offers no duplicate', async ({ page }) => {
  ui = await startUI([], 0);
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit')).toHaveCount(3);
  await expect(page.getByRole('button', { name: /^Duplicate/ })).toHaveCount(0);
});

test('the tab list reaches the page to create with the arrow keys', async ({ page }) => {
  ui = await startUI(['--edit'], 0);
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit')).toHaveCount(3);
  await page.focus('#tab-overview');
  for (let i = 0; i < 5; i++) { await page.keyboard.press('ArrowDown'); } // runs, NATS, settings, credentials, create
  await expect(page.locator('#tab-create')).toBeFocused();
  await expect(page.locator('#page-create')).toBeVisible();
});

for (const scheme of ['light', 'dark'] as const) {
  for (const lang of ['en', 'de']) {
    test(`the page to create has no accessibility violations (${scheme}, ${lang})`, async ({ browser }) => {
      ui = await startUI(['--edit'], 0);
      const ctx = await browser.newContext({ colorScheme: scheme, locale: lang });
      const page = await ctx.newPage();
      await page.goto(ui.start);
      await expect(page.locator('#machines tr.unit')).toHaveCount(3);
      await page.click('#tab-create');
      for (const kind of ['machine', 'group']) {
        await page.selectOption('#c-kind', kind);
        await page.fill('#c-name', 'x1');
        await page.click('#c-preview');
        await expect(page.locator('#c-result')).toBeVisible();
        const result = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'best-practice']).analyze();
        expect(result.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(' ')).join(', ')}`), `${kind} ${scheme} ${lang}`).toEqual([]);
      }
      await ctx.close();
    });
  }
}
