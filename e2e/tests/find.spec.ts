import { expect, test } from '@playwright/test';
import { startUI, UI } from '../fixture';

// the page of the search for a server type, and the options for runs: what the page starts, the real viper takes
let ui: UI;
test.afterEach(async () => { await ui.stop(); });

test('the question is checked before it is sent, and a location is written in small letters', async ({ page }) => {
  ui = await startUI([], 0);
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit')).toHaveCount(3);
  await page.click('#tab-find');
  await page.click('#find-go');
  await expect(page.locator('#find-status')).toContainText('at least one of CPUs, RAM and disk');
  await page.fill('#f-cpu', '2');
  await page.fill('#f-location', 'FSN1');
  await page.click('#find-go');
  await expect(page.locator('#find-status')).toContainText('needs a provider');
  const bodies: Array<Record<string, unknown>> = [];
  await page.route('**/api/run', (r) => { bodies.push(r.request().postDataJSON()); r.fulfill({ status: 400, contentType: 'application/json', body: '{"error":"refused for the test"}' }); });
  await page.selectOption('#f-provider', 'hetzner');
  await page.fill('#f-ram', '0.5');
  await page.fill('#f-vat', '0');
  await page.selectOption('#f-arch', 'arm');
  await page.click('#find-go');
  await expect(page.locator('#find-status')).toContainText('refused for the test');
  expect(bodies[0]).toEqual({ action: 'find', cpu: 2, ram: 0.5, disk: 0, arch: 'arm', provider: 'hetzner', location: 'fsn1', vat: 0 });
});

test('a search is a run of the real viper: its command line is taken, its output is in the area, it can be cancelled', async ({ page }) => {
  test.setTimeout(120_000);
  ui = await startUI([], 0);
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit')).toHaveCount(3);
  await page.click('#tab-find');
  await page.fill('#f-cpu', '2');
  await page.selectOption('#f-provider', 'hetzner');
  await page.fill('#f-location', 'fsn1');
  await page.fill('#f-vat', '19');
  await page.click('#find-go');
  await expect(page.locator('#find-out')).toContainText('$ viper find --cpu=2 --provider=hetzner --location=fsn1 --vat=19');
  await expect(page.locator('#page-find')).toBeVisible(); // the page stays
  // the network may answer or not: the search ends, or it is cancelled; either way viper took the options
  const ended = await page.locator('#find-go').isEnabled({ timeout: 20_000 }).catch(() => false);
  if (!ended) {
    await expect(page.locator('#find-stop')).toBeEnabled();
    await page.click('#find-stop');
    await expect(page.locator('#find-status')).toContainText(/Cancelled|Done|No server type|refused/, { timeout: 40_000 });
  }
  await expect(page.locator('#find-go')).toBeEnabled({ timeout: 40_000 });
  const out = await page.locator('#find-out').innerText();
  expect(out).not.toMatch(/needs a|unknown option|not an option|usage:/i);
});

test('find also runs when the page is read only', async ({ page }) => {
  ui = await startUI(['--readonly'], 0);
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit')).toHaveCount(3);
  await page.click('#tab-find');
  await expect(page.locator('#find-go')).toBeEnabled();
  const bodies: Array<Record<string, unknown>> = [];
  await page.route('**/api/run', (r) => { bodies.push(r.request().postDataJSON()); r.fulfill({ status: 400, contentType: 'application/json', body: '{"error":"x"}' }); });
  await page.fill('#f-ram', '8');
  await page.click('#find-go');
  await expect.poll(() => bodies.length).toBe(1);
  // the options that change something are off
  await page.click('#tab-overview');
  await page.locator('#opt-button').click();
  await expect(page.locator('#opt-force')).toBeDisabled();
  await expect(page.locator('#opt-registry')).toBeDisabled();
});

test('the options of runs reach viper: parallel for a group, force and the registry for down, and force is for one request', async ({ page }) => {
  test.setTimeout(120_000);
  ui = await startUI([], 0);
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit')).toHaveCount(3);
  await expect(page.locator('#opt-active')).toHaveText('');
  await page.locator('#opt-button').click();
  await page.fill('#opt-parallel', '2');
  await expect(page.locator('#opt-active')).toHaveText('(1 set)');
  await page.keyboard.press('Escape'); // the menu closes and the buttons below are free
  await expect(page.locator('#opt-panel')).toBeHidden();
  await page.getByRole('button', { name: 'Up group dev', exact: true }).click();
  await expect(page.locator('#run-output')).toContainText('$ viper up --group dev --parallel=2');
  await expect(page.locator('#run-status')).toContainText(/Ended|exit/, { timeout: 60_000 });
  expect(await page.locator('#run-output').innerText()).not.toMatch(/whole number|unknown option|exclude each other/i);
  // down of one machine: parallel is for groups only, force and the registry go along, and the box is clear afterwards
  await page.click('#tab-overview');
  await page.click('#opt-button');
  await page.check('#opt-force');
  await page.selectOption('#opt-registry', 'keep');
  await expect(page.locator('#opt-active')).toHaveText('(3 set)');
  await page.keyboard.press('Escape');
  await page.getByRole('button', { name: 'Down web', exact: true }).click();
  await expect(page.locator('#run-output')).toContainText('$ viper down web --force --keep-registry');
  await expect(page.locator('#opt-force')).not.toBeChecked();
  await expect(page.locator('#opt-active')).toHaveText('(1 set)'); // force and registry are back, parallel stays
  expect(await page.locator('#run-output').innerText()).not.toMatch(/whole number|unknown option|exclude each other/i);
  await page.click('#run-cancel', { timeout: 3000 }).catch(() => {}); // down asks for DELETE and waits; the run may also be over already
});
