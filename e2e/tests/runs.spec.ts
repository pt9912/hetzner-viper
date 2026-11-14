import { expect, test } from '@playwright/test';
import { startUI, UI } from '../fixture';

// The questions and the cancel: the page against a server that is answered by the test (the routes), so that the
// behaviour of the page itself is what is looked at. The server side has its own tests (Go).
let ui: UI;
// the key works once: every test has a server of its own
test.beforeEach(async () => { ui = await startUI(); });
test.afterEach(async () => { await ui.stop(); });

function sse(parts: Array<[string, unknown]>): string {
  return parts.map(([k, d]) => `event: ${k}\ndata: ${JSON.stringify(d)}\n\n`).join('');
}

test('a question gets an input field and the answer is what the person types', async ({ page }) => {
  await page.goto(ui.start);
  const answers: unknown[] = [];
  await page.route('**/api/run', (r) => r.fulfill({ contentType: 'application/json', body: '{"id":"abc"}' }));
  await page.route('**/api/runs', (r) => r.fulfill({ contentType: 'application/json', body: '{"runs":[{"id":"abc","action":"down","machine":"web","state":"waiting","code":0}]}' }));
  await page.route('**/api/events?id=abc', (r) => r.fulfill({
    contentType: 'text/event-stream',
    body: sse([['out', { seq: 0, text: '$ viper down web\n' }], ['out', { seq: 1, text: 'Type DELETE to continue: ' }], ['state', { state: 'waiting', code: 0 }]]),
  }));
  await page.route('**/api/answer', (r) => { answers.push(r.request().postDataJSON()); r.fulfill({ contentType: 'application/json', body: '{"ok":true}' }); });
  await page.getByRole('button', { name: 'Down web', exact: true }).click();
  await expect(page.locator('#run-output')).toContainText('Type DELETE to continue:');
  await expect(page.locator('#run-form')).toBeVisible();
  await expect(page.locator('#run-status')).toContainText('Waiting for your answer');
  await expect(page.locator('#run-answer')).toBeFocused();
  await page.fill('#run-answer', 'DELETE');
  await page.press('#run-answer', 'Enter');
  await expect.poll(() => answers.length).toBe(1);
  expect(answers[0]).toEqual({ id: 'abc', text: 'DELETE' });
  await expect(page.locator('#run-answer')).toHaveValue('');
});

test('output from a run is shown as text, never as markup', async ({ page }) => {
  await page.goto(ui.start);
  await page.route('**/api/run', (r) => r.fulfill({ contentType: 'application/json', body: '{"id":"xss"}' }));
  await page.route('**/api/runs', (r) => r.fulfill({ contentType: 'application/json', body: '{"runs":[]}' }));
  await page.route('**/api/events?id=xss', (r) => r.fulfill({
    contentType: 'text/event-stream',
    body: sse([['out', { seq: 0, text: '<img src=x onerror="window.pwned=1"><script>window.pwned=2</script>\n' }], ['state', { state: 'done', code: 1 }]]),
  }));
  await page.getByRole('button', { name: 'Up web', exact: true }).click();
  await expect(page.locator('#run-output')).toContainText('<img src=x');
  expect(await page.evaluate(() => (window as unknown as { pwned?: number }).pwned)).toBeUndefined();
  await expect(page.locator('#run-output img')).toHaveCount(0);
  await expect(page.locator('#run-status')).toContainText('Ended: exit 1');
});

test('cancel is Ctrl-C: the page says it ends and shows the exit status', async ({ page }) => {
  await page.goto(ui.start);
  await page.getByRole('button', { name: 'Up web', exact: true }).click();
  await expect(page.locator('#run-status')).toContainText('Running');
  await expect(page.locator('#run-cancel')).toBeEnabled();
  await page.click('#run-cancel');
  await expect(page.locator('#run-status')).toContainText('Ending');
  await expect(page.locator('#run-status')).toContainText('exit 130', { timeout: 40_000 });
  await expect(page.locator('#run-cancel')).toBeDisabled();
  // and a new page finds the run again
  const page2 = await page.context().newPage();
  await page2.goto(ui.base + '/', { waitUntil: 'commit' }); // Playwright's Firefox does not report the load of a second page next to a page with an open stream
  await page2.click('#tab-runs');
  await expect(page2.locator('#run-list li')).toHaveCount(1);
});

test('a second run of the same machine is refused with the reason', async ({ page }) => {
  await page.goto(ui.start);
  await page.getByRole('button', { name: 'Up a', exact: true }).first().click();
  await expect(page.locator('#run-status')).toContainText('Running');
  await page.click('#tab-overview');
  await page.getByRole('button', { name: 'Down a', exact: true }).first().click();
  await expect(page.locator('#run-status')).toContainText('Not started');
  await expect(page.locator('#run-status')).toContainText('a run holds the machine a');
  await page.click('#run-cancel');
  await expect(page.locator('#run-status')).toContainText('exit 130', { timeout: 40_000 });
});

test.describe('read only', () => {
  let ro: UI;
  test.beforeAll(async () => { ro = await startUI(['--readonly']); });
  test.afterAll(async () => { await ro.stop(); });
  test('offers no action', async ({ page }) => {
    await page.goto(ro.start);
    await expect(page.locator('#machines tbody tr')).toHaveCount(3);
    await expect(page.getByRole('button', { name: 'Up web', exact: true })).toBeDisabled();
    await expect(page.getByRole('button', { name: 'Down web', exact: true })).toBeDisabled();
    await expect(page.getByRole('button', { name: 'Check web', exact: true })).toBeEnabled();
    await expect(page.locator('#version')).toContainText('read only');
  });
});
