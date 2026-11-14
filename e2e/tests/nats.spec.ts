import { expect, test } from '@playwright/test';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import { startUI, UI } from '../fixture';

let ui: UI;
test.beforeEach(async () => { ui = await startUI(); });
test.afterEach(async () => { await ui.stop(); });

function sse(parts: Array<[string, unknown]>): string {
  return parts.map(([k, d]) => `event: ${k}\ndata: ${JSON.stringify(d)}\n\n`).join('');
}

test('the NATS page shows the group, its machines and how viper nats will speak', async ({ page }) => {
  await page.goto(ui.start);
  await page.click('#tab-nats');
  await expect(page.locator('#nats-group')).toHaveValue('dev');
  await expect(page.locator('#nats-to option')).toHaveText(['a', 'b']);
  await expect(page.locator('#nats-mode')).toContainText('No key of the operator');
  await expect(page.locator('#nats-from option').first()).toContainText('a machine of the group');
});

test('peers is a real run of viper nats; it ends with the exit status (no machine is up)', async ({ page }) => {
  await page.goto(ui.start);
  await page.click('#tab-nats');
  await page.fill('#nats-timeout', '3');
  await page.click('#nats-peers');
  await expect(page.locator('#page-runs')).toBeVisible();
  await expect(page.locator('#run-output')).toContainText('$ viper nats peers dev --timeout=3 --json');
  await expect(page.locator('#run-status')).toContainText('Ended: exit 1', { timeout: 30_000 });
});

test('a message is sent by text and file: the file is handed over first, the text goes in the request', async ({ page }) => {
  await page.goto(ui.start);
  await page.click('#tab-nats');
  const seen: { run?: Record<string, unknown>; uploadUrl?: string; uploadBody?: string } = {};
  await page.route('**/api/upload?**', async (route) => {
    seen.uploadUrl = route.request().url();
    seen.uploadBody = route.request().postData() ?? '';
    await route.continue(); // the real server takes it
  });
  await page.route('**/api/run', (route) => {
    seen.run = route.request().postDataJSON();
    route.fulfill({ contentType: 'application/json', body: '{"id":"msg"}' });
  });
  await page.route('**/api/runs', (r) => r.fulfill({ contentType: 'application/json', body: '{"runs":[{"id":"msg","action":"nats","sub":"send","nats":"dev","state":"done","code":0}]}' }));
  await page.route('**/api/events?id=msg', (r) => r.fulfill({ contentType: 'text/event-stream', body: sse([['out', { seq: 0, text: '{"event":"sent","att_id":"o1","att_name":"r.pdf","att_type":"pdf","att_size":12}\n' }], ['state', { state: 'done', code: 0 }]]) }));
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'e2e-file-'));
  const file = path.join(dir, 'report.pdf');
  fs.writeFileSync(file, '%PDF-1.4 e2e');
  await page.selectOption('#nats-to', 'b');
  await page.fill('#nats-text', 'please look; $(rm -rf /)');
  await page.setInputFiles('#nats-file', file);
  await page.click('#nats-send button[type=submit]');
  await expect(page.locator('#run-output')).toContainText('sent [file r.pdf, pdf, 12 bytes]');
  expect(seen.uploadUrl).toContain('kind=pdf');
  expect(seen.uploadUrl).toContain('name=report.pdf');
  expect(seen.uploadBody).toBe('%PDF-1.4 e2e');
  expect(seen.run).toMatchObject({ action: 'nats', sub: 'send', nats: 'dev', to: 'b', text: 'please look; $(rm -rf /)' });
  expect(typeof seen.run?.upload).toBe('string');
  fs.rmSync(dir, { recursive: true, force: true });
});

test('what listen shows is text; a file in a message offers Fetch, and a fetched file is a download', async ({ page }) => {
  await page.goto(ui.start);
  await page.click('#tab-nats');
  const bodies: Array<Record<string, unknown>> = [];
  let n = 0;
  await page.route('**/api/run', (route) => {
    bodies.push(route.request().postDataJSON());
    n += 1;
    route.fulfill({ contentType: 'application/json', body: JSON.stringify({ id: n === 1 ? 'lis' : 'fet' }) });
  });
  await page.route('**/api/runs', (r) => r.fulfill({ contentType: 'application/json', body: '{"runs":[{"id":"lis","action":"nats","sub":"listen","nats":"dev","state":"running","code":0},{"id":"fet","action":"nats","sub":"fetch","nats":"dev","state":"done","code":0}]}' }));
  const sha = 'ab'.repeat(32);
  const msg = { event: 'message', via: 'out', from: 'a', to: 'b', text: '<img src=x onerror=window.pwned=1>', att_id: 'obj_9', att_name: 'p.pdf', att_type: 'pdf', att_size: 5, att_sha256: sha };
  await page.route('**/api/events?id=lis', (r) => r.fulfill({ contentType: 'text/event-stream', body: sse([['out', { seq: 0, text: JSON.stringify(msg) + '\n' }], ['state', { state: 'running', code: 0 }]]) }));
  await page.route('**/api/events?id=fet', (r) => r.fulfill({ contentType: 'text/event-stream', body: sse([['out', { seq: 0, text: '{"event":"fetched","text":"/tmp/x/p.pdf"}\n' }], ['state', { state: 'done', code: 0 }]]) }));
  await page.fill('#nats-listen-topic', 'build');
  await page.click('#nats-listen');
  await expect(page.locator('#run-output')).toContainText('a -> b: <img src=x onerror=window.pwned=1> [file p.pdf, pdf, 5 bytes]');
  await expect(page.locator('#run-output img')).toHaveCount(0);
  expect(await page.evaluate(() => (window as unknown as { pwned?: number }).pwned)).toBeUndefined();
  expect(bodies[0]).toMatchObject({ sub: 'listen', topic: 'build', nats: 'dev' });
  await page.click('#run-output button');
  await expect(page.locator('#nats-id')).toHaveValue('obj_9');
  await expect(page.locator('#nats-sha')).toHaveValue(sha);
  await page.click('#nats-fetch button[type=submit]');
  expect(bodies[1]).toMatchObject({ sub: 'fetch', id: 'obj_9', sha256: sha });
  await expect(page.locator('#run-download')).toBeVisible();
  await expect(page.locator('#run-download-link')).toHaveAttribute('href', '/api/download?id=fet');
});

test('read only: the page cannot hand over a file', async () => {
  await ui.stop();
  ui = await startUI(['--readonly']);
  const res = await fetch(ui.start, { redirect: 'manual' });
  const cookie = (res.headers.get('set-cookie') ?? '').split(';')[0];
  const up = await fetch(ui.base + '/api/upload?kind=pdf', { method: 'POST', headers: { cookie, 'Content-Type': 'application/octet-stream', 'X-Viper-UI': '1', Origin: ui.base }, body: 'x' });
  expect(up.status).toBe(403);
});

test('a group without an object store takes no file', async ({ page }) => {
  await ui.stop();
  ui = await startUI([], 30, false, { objects: false });
  await page.goto(ui.start);
  await page.click('#tab-nats');
  await expect(page.locator('#nats-file')).toBeDisabled();
  await expect(page.locator('#nats-nofile')).toBeVisible();
});
