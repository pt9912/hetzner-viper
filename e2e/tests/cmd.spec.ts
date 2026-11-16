import AxeBuilder from '@axe-core/playwright';
import { expect, test } from '@playwright/test';
import { startUI, UI } from '../fixture';

// a command on a machine (viper ssh --cmd) and the start of VS Code: what the page starts, the real viper takes
let ui: UI;
test.afterEach(async () => { await ui.stop(); });

test('the page of the command is only there with --edit, and the command is checked before it is sent', async ({ page }) => {
  ui = await startUI([], 0);
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit')).toHaveCount(3);
  await expect(page.locator('#tab-cmd')).toBeHidden();
  await page.click('#tab-image');
  await page.keyboard.press('ArrowDown'); // the hidden tab of the command is skipped
  await expect(page.locator('#tab-settings')).toBeFocused();
  await expect(page.locator('#page-cmd')).toBeHidden();
  await ui.stop();
  ui = await startUI(['--edit'], 0);
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit')).toHaveCount(3);
  await page.click('#tab-cmd');
  await page.click('#cmd-go');
  await expect(page.locator('#cmd-status')).toContainText('Give the command');
  await page.fill('#cmd-text', '-oProxyCommand=id');
  await page.fill('#cmd-word', 'run');
  await page.click('#cmd-go');
  await expect(page.locator('#cmd-status')).toContainText('dash');
  await page.fill('#cmd-text', 'uptime');
  await page.fill('#cmd-word', 'ru');
  await page.click('#cmd-go');
  await expect(page.locator('#cmd-status')).toContainText('Type the word run');
});

test('a command is a run of the real viper: one word after --cmd=, and the options of the menu go along for a group', async ({ page }) => {
  test.setTimeout(120_000);
  ui = await startUI(['--edit'], 0);
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit')).toHaveCount(3);
  await page.click('#tab-cmd');
  await page.selectOption('#cmd-target', { label: 'web' });
  await page.fill('#cmd-text', 'echo "a b"; uptime');
  await page.fill('#cmd-word', 'run');
  await page.click('#cmd-go');
  await expect(page.locator('#run-output')).toContainText('$ viper ssh web --cmd=echo "a b"; uptime');
  // viper took it: it says that the machine has no SSH configuration, not that an option is wrong
  await expect(page.locator('#run-output')).toContainText('No SSH config', { timeout: 30_000 });
  expect(await page.locator('#run-output').innerText()).not.toMatch(/unknown option|usage:|not an option/i);
  await expect(page.locator('#cmd-word')).toHaveValue('');
  // a group: every machine, with --parallel from the menu
  await page.click('#tab-cmd');
  await page.selectOption('#cmd-target', { label: 'every machine of dev' });
  await page.fill('#cmd-text', 'uptime');
  await page.fill('#cmd-word', 'run');
  await page.click('#tab-overview');
  await page.click('#opt-button');
  await page.fill('#opt-parallel', '2');
  await page.keyboard.press('Escape');
  await page.click('#tab-cmd');
  await page.click('#cmd-go');
  await expect(page.locator('#run-output')).toContainText('$ viper ssh --group dev --parallel=2 --cmd=uptime');
});

test('VS Code has a button for a machine that is started, and it starts viper vscode', async ({ page }) => {
  ui = await startUI([], 0);
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit')).toHaveCount(3);
  const overview = await page.evaluate(() => fetch('/api/overview', { credentials: 'same-origin' }).then((r) => r.json()));
  for (const m of overview.machines) { if (m.name === 'web') { m.state = 'ready'; } }
  await page.route('**/api/overview', (route) => route.fulfill({ json: overview }));
  await page.reload();
  await expect(page.locator('#machines tr.unit')).toHaveCount(3);
  await expect(page.getByRole('button', { name: 'VS Code on web', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: /VS Code on (a|b)$/ })).toHaveCount(0);
  await page.getByRole('button', { name: 'VS Code on web', exact: true }).click();
  await expect(page.locator('#run-output')).toContainText('$ viper vscode web');
  await expect(page.locator('#run-output')).toContainText('No SSH config', { timeout: 30_000 });
});

for (const scheme of ['light', 'dark'] as const) {
  for (const lang of ['en', 'de']) {
    test(`the page of the command has no accessibility violations (${scheme}, ${lang})`, async ({ browser }) => {
      ui = await startUI(['--edit'], 0);
      const ctx = await browser.newContext({ colorScheme: scheme, locale: lang });
      const page = await ctx.newPage();
      await page.goto(ui.start);
      await expect(page.locator('#machines tr.unit')).toHaveCount(3);
      await page.click('#tab-cmd');
      const result = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'best-practice']).analyze();
      expect(result.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(' ')).join(', ')}`), `${scheme} ${lang}`).toEqual([]);
      await ctx.close();
    });
  }
}

test('the button Terminal opens a window of the desktop that runs viper terminal --here with the settings directory as an absolute path', async ({ page }) => {
  test.skip(process.platform !== 'linux', 'the fake terminal program is for Linux');
  ui = await startUI([], 0, false, { terminal: true });
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit')).toHaveCount(3);
  const overview = await page.evaluate(() => fetch('/api/overview', { credentials: 'same-origin' }).then((r) => r.json()));
  for (const m of overview.machines) { if (m.name === 'web') { m.state = 'ready'; } }
  await page.route('**/api/overview', (route) => route.fulfill({ json: overview }));
  await page.reload();
  await expect(page.locator('#machines tr.unit')).toHaveCount(3);
  await expect(page.getByRole('button', { name: 'Terminal on web', exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Terminal on web', exact: true }).click();
  await expect(page.locator('#run-output')).toContainText('$ viper terminal web');
  const log = `${ui.root}/terminal.log`;
  const fs = await import('node:fs');
  await expect.poll(() => fs.existsSync(log), { timeout: 20_000 }).toBe(true);
  const args = fs.readFileSync(log, 'utf8').trim().split('\n');
  // gnome-terminal -- VIPER terminal web --here --dir DIR
  expect(args[0]).toBe('--');
  expect(args.slice(2, 6)).toEqual(['terminal', 'web', '--here', '--dir']);
  expect(args[6]).toBe(ui.work);
  expect(args[1]).toMatch(/viper(\.exe)?$/);
});
