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
  await expect(page.locator('#machines tr.unit')).toHaveCount(3); // the exchange page has moved on: routes are set on the page that stays
  await page.click('#tab-nats');
  await expect(page.locator('#nats-group')).toHaveValue('dev');
  await expect(page.locator('#nats-to option')).toHaveText(['a', 'b']);
  await expect(page.locator('#nats-mode')).toContainText('No key of the operator');
  await expect(page.locator('#nats-from option').first()).toContainText('a machine of the group');
});

test('peers is a real run of viper nats; it ends with the exit status (no machine is up)', async ({ page }) => {
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit')).toHaveCount(3); // the exchange page has moved on: routes are set on the page that stays
  await page.click('#tab-nats');
  await page.fill('#nats-timeout', '3');
  await page.click('#nats-peers');
  await expect(page.locator('#page-runs')).toBeVisible();
  await expect(page.locator('#run-output')).toContainText('$ viper nats peers dev --timeout=3 --json');
  await expect(page.locator('#run-status')).toContainText('Ended: exit 1', { timeout: 30_000 });
});

test('a message is sent by text and file: the file is handed over first, the text goes in the request', async ({ page, browserName }) => {
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit')).toHaveCount(3); // the exchange page has moved on: routes are set on the page that stays
  await page.click('#tab-nats');
  const seen: { run?: Record<string, unknown>; uploadUrl?: string; uploadBody?: string } = {};
  await page.route('**/api/upload?**', async (route) => {
    seen.uploadUrl = route.request().url();
    seen.uploadBody = route.request().postDataBuffer()?.toString() ?? '';
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
  // (Playwright's WebKit does not show the body of a file that fetch sends; the id of the upload below says that the server took it)
  if (browserName !== 'webkit') { expect(seen.uploadBody).toBe('%PDF-1.4 e2e'); }
  expect(seen.run).toMatchObject({ action: 'nats', sub: 'send', nats: 'dev', to: 'b', text: 'please look; $(rm -rf /)' });
  expect(typeof seen.run?.upload).toBe('string');
  fs.rmSync(dir, { recursive: true, force: true });
});

test('what listen shows is text; a file in a message offers Fetch, and a fetched file is a download', async ({ page }) => {
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit')).toHaveCount(3); // the exchange page has moved on: routes are set on the page that stays
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
  // the messages are in the area of the page of NATS, which stays the page
  await expect(page.locator('#page-nats')).toBeVisible();
  await expect(page.locator('#nats-listen-out')).toContainText('a -> b: <img src=x onerror=window.pwned=1> [file p.pdf, pdf, 5 bytes]');
  await expect(page.locator('#nats-listen-out img')).toHaveCount(0);
  await expect(page.locator('#nats-listen-status')).toContainText('Listening to the group dev');
  await expect(page.locator('#nats-listen-stop')).toBeEnabled();
  await expect(page.locator('#nats-listen')).toBeDisabled();
  expect(await page.evaluate(() => (window as unknown as { pwned?: number }).pwned)).toBeUndefined();
  expect(bodies[0]).toMatchObject({ sub: 'listen', topic: 'build', nats: 'dev' });
  await page.click('#nats-listen-out button');
  await expect(page.locator('#nats-id')).toHaveValue('obj_9');
  await expect(page.locator('#nats-sha')).toHaveValue(sha);
  await page.click('#nats-fetch button[type=submit]');
  expect(bodies[1]).toMatchObject({ sub: 'fetch', id: 'obj_9', sha256: sha });
  await expect(page.locator('#run-download')).toBeVisible();
  await expect(page.locator('#run-download-link')).toHaveAttribute('href', '/api/download?id=fet');
});

test('listening has an area of its own: start, stop, clear, and a page that is opened again follows the run that goes on', async ({ page }) => {
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit')).toHaveCount(3);
  let cancelled = false;
  const cancels: Array<Record<string, unknown>> = [];
  const msg = (text: string) => JSON.stringify({ event: 'message', via: 'out', from: 'a', to: 'b', text }) + '\n';
  await page.route('**/api/runs', (r) => r.fulfill({ contentType: 'application/json', body: '{"runs":[{"id":"lis","action":"nats","sub":"listen","nats":"dev","state":"' + (cancelled ? 'done' : 'running') + '","code":0}]}' }));
  await page.route('**/api/events?id=lis', (r) => r.fulfill({
    contentType: 'text/event-stream',
    body: cancelled ? sse([['out', { seq: 1, text: msg('after') }], ['state', { state: 'done', code: 130 }]]) : sse([['out', { seq: 0, text: msg('first') }], ['state', { state: 'running', code: 0 }]]),
  }));
  await page.route('**/api/cancel', (r) => { cancels.push(r.request().postDataJSON()); cancelled = true; r.fulfill({ contentType: 'application/json', body: '{"ok":true}' }); });
  // a listen run that is going when the page of NATS is opened: the area follows it, without the person pressing anything
  await page.click('#tab-nats');
  await expect(page.locator('#nats-listen-out')).toContainText('a -> b: first');
  await expect(page.locator('#nats-listen-status')).toContainText('Listening to the group dev');
  await expect(page.locator('#nats-listen')).toBeDisabled();
  await expect(page.locator('#nats-listen-stop')).toBeEnabled();
  // clear
  await page.click('#nats-listen-clear');
  await expect(page.locator('#nats-listen-out')).toHaveText('');
  // stop: viper nats is ended like Ctrl-C, and the area says so
  await page.click('#nats-listen-stop');
  await expect.poll(() => cancels.length).toBe(1);
  expect(cancels[0]).toEqual({ id: 'lis' });
  await expect(page.locator('#nats-listen-status')).toContainText('Stopped listening');
  await expect(page.locator('#nats-listen')).toBeEnabled();
  await expect(page.locator('#nats-listen-stop')).toBeDisabled();
  await expect(page.locator('#page-nats')).toBeVisible();
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
  await expect(page.locator('#machines tr.unit')).toHaveCount(3); // the exchange page has moved on: routes are set on the page that stays
  await page.click('#tab-nats');
  await expect(page.locator('#nats-file')).toBeDisabled();
  await expect(page.locator('#nats-nofile')).toBeVisible();
});

test('a request waits 30 seconds for its answer, and what was sent is not sent again by the next click', async ({ page }) => {
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit')).toHaveCount(3); // the exchange page has moved on: routes are set on the page that stays
  await page.click('#tab-nats');
  const bodies: Array<Record<string, unknown>> = [];
  await page.route('**/api/run', (route) => {
    bodies.push(route.request().postDataJSON());
    route.fulfill({ contentType: 'application/json', body: '{"id":"rq"}' });
  });
  await page.route('**/api/runs', (r) => r.fulfill({ contentType: 'application/json', body: '{"runs":[]}' }));
  await page.route('**/api/events?id=rq', (r) => r.fulfill({ contentType: 'text/event-stream', body: sse([['state', { state: 'done', code: 0 }]]) }));
  await expect(page.locator('#nats-reqtimeout')).toBeDisabled(); // only a request waits
  await page.selectOption('#nats-kind', 'request');
  await expect(page.locator('#nats-reqtimeout')).toHaveValue('30');
  await page.fill('#nats-text', 'status?');
  await page.click('#nats-send button[type=submit]');
  await expect.poll(() => bodies.length).toBe(1);
  expect(bodies[0]).toMatchObject({ sub: 'request', to: 'a', timeout: 30, text: 'status?' });
  await page.click('#tab-nats');
  await expect(page.locator('#nats-text')).toHaveValue('');
});

test('a stream that the server refuses says that the session is gone', async ({ page }) => {
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit')).toHaveCount(3); // the exchange page has moved on: routes are set on the page that stays
  await page.route('**/api/run', (r) => r.fulfill({ contentType: 'application/json', body: '{"id":"gone"}' }));
  await page.route('**/api/runs', (r) => r.fulfill({ status: 403, contentType: 'text/plain', body: 'forbidden' }));
  await page.route('**/api/events?id=gone', (r) => r.fulfill({ status: 403, contentType: 'text/plain', body: 'forbidden' }));
  await page.getByRole('button', { name: 'Preflight web', exact: true }).click();
  await expect(page.locator('#banner')).toBeVisible();
  await expect(page.locator('#banner')).toContainText('session has ended');
});

test('the operator is registered by one click, and removed after a second look', async ({ page }) => {
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit')).toHaveCount(3);
  const bodies: Array<Record<string, unknown>> = [];
  await page.route('**/api/run', (route) => {
    bodies.push(route.request().postDataJSON());
    route.fulfill({ contentType: 'application/json', body: '{"id":"op"}' });
  });
  await page.route('**/api/runs', (r) => r.fulfill({ contentType: 'application/json', body: '{"runs":[]}' }));
  await page.route('**/api/events?id=op', (r) => r.fulfill({ contentType: 'text/event-stream', body: sse([['out', { seq: 0, text: 'The operator operator is registered.\n' }], ['state', { state: 'done', code: 0 }]]) }));
  await page.click('#tab-nats');
  // no key of the operator here: it can be registered, not removed
  await expect(page.locator('#nats-init')).toBeVisible();
  await expect(page.locator('#nats-revoke')).toBeHidden();
  await page.click('#nats-init');
  await expect.poll(() => bodies.length).toBe(1);
  expect(bodies[0]).toEqual({ action: 'nats', nats: 'dev', sub: 'init' });
  await expect(page.locator('#run-output')).toContainText('The operator operator is registered.');
  // a computer with the key: the other way round, and the removal asks once more
  // (the real overview, read by the page itself, with the key of the operator added)
  const overview = await page.evaluate(() => fetch('/api/overview', { credentials: 'same-origin' }).then((r) => r.json()));
  for (const g of overview.nats) { g.operator = true; }
  await page.route('**/api/overview', (route) => route.fulfill({ json: overview }));
  await page.reload();
  await page.click('#tab-nats');
  await expect(page.locator('#nats-init')).toBeHidden();
  await expect(page.locator('#nats-revoke')).toBeVisible();
  await page.click('#nats-revoke');
  expect(bodies.length).toBe(1); // the first click only asks
  await expect(page.locator('#nats-revoke')).toContainText('Sure?');
  await page.click('#nats-revoke');
  await expect.poll(() => bodies.length).toBe(2);
  expect(bodies[1]).toEqual({ action: 'nats', nats: 'dev', sub: 'revoke' });
});

// a file or a text that is dropped or pasted goes where the file dialog and the keyboard would put it
async function dropOnSend(page: import('@playwright/test').Page, files: Array<{ name: string; type: string; content: string }>) {
  const dt = await page.evaluateHandle((fs) => {
    const d = new DataTransfer();
    for (const f of fs) { d.items.add(new File([f.content], f.name, { type: f.type })); }
    return d;
  }, files);
  await page.dispatchEvent('#nats-send', 'drop', { dataTransfer: dt });
}

test('a PDF dropped on the form is the file of the message, a text file is its text, anything else is said to be unusable', async ({ page }) => {
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit')).toHaveCount(3);
  await page.click('#tab-nats');
  await dropOnSend(page, [{ name: 'plan.pdf', type: 'application/pdf', content: '%PDF-1.4 x' }]);
  await expect(page.locator('#nats-file')).toHaveJSProperty('files.length', 1);
  expect(await page.locator('#nats-file').evaluate((i: HTMLInputElement) => i.files?.[0].name)).toBe('plan.pdf');
  await expect(page.locator('#nats-send-status')).toContainText('File taken: plan.pdf');
  await dropOnSend(page, [{ name: 'notes.txt', type: 'text/plain', content: 'look at this' }]);
  await expect(page.locator('#nats-text')).toHaveValue('look at this');
  await expect(page.locator('#nats-send-status')).toContainText('Text taken from notes.txt');
  await dropOnSend(page, [{ name: 'run.exe', type: 'application/x-msdownload', content: 'MZ' }]);
  await expect(page.locator('#nats-send-status')).toContainText('Not usable: run.exe');
  expect(await page.locator('#nats-file').evaluate((i: HTMLInputElement) => i.files?.[0].name)).toBe('plan.pdf'); // the file stays
  await dropOnSend(page, [{ name: 'a.zip', type: 'application/zip', content: 'PK' }, { name: 'b.zip', type: 'application/zip', content: 'PK' }]);
  await expect(page.locator('#nats-send-status')).toContainText('Only one file goes with a message');
  expect(await page.locator('#nats-file').evaluate((i: HTMLInputElement) => i.files?.[0].name)).toBe('a.zip');
  await page.click('#nats-file-clear');
  await expect(page.locator('#nats-file')).toHaveJSProperty('files.length', 0);
});

test('a file that is larger than the group takes, or is for a group without an object store, is not taken', async ({ page }) => {
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit')).toHaveCount(3);
  await page.click('#tab-nats');
  const big = 'x'.repeat(9 * 1024 * 1024); // the group takes 8 MiB by default
  await dropOnSend(page, [{ name: 'big.zip', type: 'application/zip', content: big }]);
  await expect(page.locator('#nats-send-status')).toContainText('is larger than the group takes (8 MiB)');
  await expect(page.locator('#nats-file')).toHaveJSProperty('files.length', 0);
});

test('a dropped file goes out with the message', async ({ page }) => {
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit')).toHaveCount(3);
  await page.click('#tab-nats');
  const seen: { uploadUrl?: string; run?: Record<string, unknown> } = {};
  await page.route('**/api/upload?**', (route) => { seen.uploadUrl = route.request().url(); return route.continue(); });
  await page.route('**/api/run', (route) => { seen.run = route.request().postDataJSON(); return route.fulfill({ contentType: 'application/json', body: '{"id":"dd"}' }); });
  await page.route('**/api/runs', (r) => r.fulfill({ contentType: 'application/json', body: '{"runs":[]}' }));
  await page.route('**/api/events?id=dd', (r) => r.fulfill({ contentType: 'text/event-stream', body: sse([['state', { state: 'done', code: 0 }]]) }));
  await dropOnSend(page, [{ name: 'shot.png', type: 'image/png', content: 'PNGDATA' }]);
  await page.fill('#nats-text', 'see the picture');
  await page.click('#nats-send button[type=submit]');
  await expect.poll(() => seen.run).toBeTruthy();
  expect(seen.uploadUrl).toContain('kind=image');
  expect(seen.uploadUrl).toContain('name=shot.png');
  expect(typeof seen.run?.upload).toBe('string');
  await page.click('#tab-nats');
  await expect(page.locator('#nats-file')).toHaveJSProperty('files.length', 0); // sent: not sent again
});

test('Ctrl+V: an image from the clipboard is a file, a text outside a field is the text', async ({ page, browserName }) => {
  // (only Chromium lets a script make a paste event that carries data)
  test.skip(browserName !== 'chromium', 'a paste event with data can only be made in Chromium');
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit')).toHaveCount(3);
  await page.click('#tab-nats');
  await page.evaluate(() => {
    const d = new DataTransfer();
    d.items.add(new File(['PNGDATA'], '', { type: 'image/png' })); // a screenshot has no name
    document.dispatchEvent(new ClipboardEvent('paste', { clipboardData: d, bubbles: true, cancelable: true }));
  });
  await expect(page.locator('#nats-send-status')).toContainText('File taken: pasted.png');
  await page.evaluate(() => {
    const d = new DataTransfer();
    d.setData('text/plain', 'from the clipboard');
    (document.activeElement as HTMLElement | null)?.blur();
    document.dispatchEvent(new ClipboardEvent('paste', { clipboardData: d, bubbles: true, cancelable: true }));
  });
  await expect(page.locator('#nats-text')).toHaveValue('from the clipboard');
});

test('nothing is taken while the page is read only or on another page', async ({ page }) => {
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit')).toHaveCount(3);
  // the NATS page is not shown: a paste or a drop is not for the form
  await page.evaluate(() => {
    const d = new DataTransfer();
    d.items.add(new File(['x'], 'x.pdf', { type: 'application/pdf' }));
    document.getElementById('nats-send')!.dispatchEvent(new DragEvent('drop', { dataTransfer: d, bubbles: true, cancelable: true }));
  });
  await expect(page.locator('#nats-file')).toHaveJSProperty('files.length', 0);
});
