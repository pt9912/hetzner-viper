import AxeBuilder from '@axe-core/playwright';
import { expect, test } from '@playwright/test';
import { startUI, UI } from '../fixture';

// viper ui-chat: the first stage (docs/plan/ui-chat.md): the page, the groups, the state of the operator, one at a time
let ui: UI;
test.afterEach(async () => { await ui.stop(); });

test('without the key of the operator the chat says what to do, and offers no conversation', async ({ page }) => {
  ui = await startUI([], 0, false, { command: 'ui-chat' });
  await page.goto(ui.start);
  await expect(page.locator('#me')).toContainText('not logged in');
  await expect(page.locator('#empty')).toContainText('You are not logged in in this group');
  await expect(page.locator('#empty code')).toHaveText('viper nats init dev');
  await expect(page.locator('.conv-btn')).toHaveCount(0);
});

test('with the key the machines of the group are conversations, and one opens', async ({ page }) => {
  ui = await startUI([], 0, false, { command: 'ui-chat', operator: true, notStarted: true });
  await page.goto(ui.start);
  await expect(page.locator('#me')).toContainText('Operator operator logged in');
  await expect(page.locator('.group h2')).toContainText('dev');
  await expect(page.locator('.group h2 .led')).toHaveClass(/off/); // nothing runs: the group is not green
  await expect(page.locator('.group h2')).toContainText('not started');
  await expect(page.locator('#banner')).toBeHidden(); // a group that is not started is a state, not a disturbance
  await expect(page.locator('.conv-btn')).toHaveCount(4); // all, two machines and their pair
  await page.locator('.conv-btn[data-key="dev/rechner__a"]').click();
  await expect(page.locator('#conv-name')).toHaveText('a');
  await expect(page.locator('#conv-sub')).toContainText('Machine of the group dev');
  await expect(page.locator('.conv-btn[data-key="dev/rechner__a"]')).toHaveAttribute('aria-current', 'true');
  await expect(page.locator('#msgs')).toHaveAttribute('role', 'log');
});

// the history on disk: what the chat shows of conversations that were kept (no NATS server here)
const ago = (min: number) => new Date(Date.now() - min * 60_000).toISOString();
const history = {
  rechner__a: [
    { n: 1, t: ago(30), dir: 'in', from: 'a', type: 'msg', text: 'build finished' },
    { n: 2, t: ago(20), dir: 'in', from: 'a', type: 'msg', text: 'see:\n```sh\nmake test\n```' },
    { n: 3, t: ago(10), dir: 'in', from: 'a', type: 'msg', text: '<img src=x onerror="window.pwned=1"> <b>bold</b> https://example.test/x' },
  ],
  paar__a__b: [
    { n: 4, t: ago(9), dir: 'in', from: 'a', to: 'b', type: 'msg', text: 'hello b' },
    { n: 5, t: ago(8), dir: 'in', from: 'b', to: 'a', type: 'msg', text: 'hello a' },
  ],
  thema__gone: [{ n: 6, t: ago(7), dir: 'in', from: 'b', topic: 'gone', type: 'msg', text: 'topic that no setting lists' }],
  alle: [
    { n: 1, t: ago(30), dir: 'in', from: 'a', type: 'msg', text: 'build finished' },
    { n: 5, t: ago(8), dir: 'in', from: 'b', to: 'a', type: 'req', id: 'q1', text: 'a question' },
    { n: 6, t: ago(7), dir: 'in', from: 'a', to: 'b', type: 'res', re: 'q1', text: 'an answer' },
  ],
};

test('the history shows in the list with unread counts, and a conversation shows as a thread of text only', async ({ page }) => {
  ui = await startUI([], 0, false, { command: 'ui-chat', operator: true, history });
  await page.goto(ui.start);
  const a = page.locator('.conv-btn[data-key="dev/rechner__a"]');
  await expect(a.locator('.badge')).toHaveText('3');
  await expect(a.locator('.pv')).toContainText('<img src=x');
  await expect(page.locator('.conv-btn[data-key="dev/thema__gone"] .nm')).toHaveText('gone'); // a topic that only the history knows
  await expect(page.locator('.conv-btn[data-key="dev/paar__a__b"] .nm')).toHaveText('a b');
  await a.click();
  await expect(page.locator('#msgs article')).toHaveCount(3);
  await expect(page.locator('#msgs pre.code')).toHaveText('make test');
  await expect(page.locator('#msgs img, #msgs b, #msgs a')).toHaveCount(0); // text stays text: no markup, no link
  await expect(page.locator('#msgs')).toContainText('<b>bold</b> https://example.test/x');
  expect(await page.evaluate(() => (window as unknown as { pwned?: number }).pwned)).toBeUndefined();
  await expect(a.locator('.badge')).toHaveCount(0); // seen
  await page.reload(); // and kept
  await expect(page.locator('.conv-btn[data-key="dev/rechner__a"] .badge')).toHaveCount(0);
  await expect(page.locator('.conv-btn[data-key="dev/paar__a__b"] .badge')).toHaveText('2');
});

test('a pair is read only with the first machine on the left, and all messages can be filtered', async ({ page }) => {
  ui = await startUI([], 0, false, { command: 'ui-chat', operator: true, history });
  await page.goto(ui.start);
  await page.locator('.conv-btn[data-key="dev/paar__a__b"]').click();
  await expect(page.locator('#conv-sub')).toContainText('Read only');
  await expect(page.locator('#msgs article.l')).toContainText('hello b');
  await expect(page.locator('#msgs article.r')).toContainText('hello a');
  await page.locator('.conv-btn[data-key="dev/alle"]').click();
  await expect(page.locator('#msgs article')).toHaveCount(3);
  await page.selectOption('#filter', 'req');
  await expect(page.locator('#msgs article')).toHaveCount(1);
  await expect(page.locator('#msgs article')).toContainText('b → a');
  await page.selectOption('#filter', 'res');
  await expect(page.locator('#msgs article .tag')).toHaveText('answer');
});

test('writing: the field is where one can write, counts bytes, keeps a draft, and an error is said', async ({ page }) => {
  ui = await startUI([], 0, false, { command: 'ui-chat', operator: true, history });
  await page.goto(ui.start);
  await page.locator('.conv-btn[data-key="dev/alle"]').click();
  await expect(page.locator('#composer')).toBeHidden();
  await page.locator('.conv-btn[data-key="dev/paar__a__b"]').click();
  await expect(page.locator('#composer')).toBeHidden();
  await page.locator('.conv-btn[data-key="dev/rechner__a"]').click();
  await expect(page.locator('#composer')).toBeVisible();
  await expect(page.locator('#send')).toBeDisabled(); // nothing to send
  await page.fill('#text', 'äö');
  await expect(page.locator('#count')).toHaveText('4 of 16384 bytes');
  await expect(page.locator('#send')).toBeEnabled();
  await page.fill('#text', 'x'.repeat(16385));
  await expect(page.locator('#send')).toBeDisabled();
  await expect(page.locator('#count')).toHaveClass(/over/);
  await page.fill('#text', 'a draft');
  await page.locator('.conv-btn[data-key="dev/thema__gone"]').click();
  await expect(page.locator('#text')).toHaveValue('');
  await expect(page.locator('#kind')).toBeHidden(); // a topic knows only the broadcast
  await page.locator('.conv-btn[data-key="dev/rechner__a"]').click();
  await expect(page.locator('#text')).toHaveValue('a draft');
  await page.selectOption('#kind', 'ask');
  await expect(page.locator('#wait')).toBeVisible();
  await page.press('#text', 'Control+Enter'); // there is no server of NATS here: the reason is said, the text stays
  await expect(page.locator('#send-status')).toContainText('Not sent:');
  await expect(page.locator('#text')).toHaveValue('a draft');
});

test('an open question of a machine can be answered, and a chat that only reads has no field', async ({ page }) => {
  const open = { rechner__a: [{ n: 1, t: ago(5), dir: 'in', from: 'a', to: 'operator', type: 'req', id: 'ab'.repeat(8), text: 'may I deploy?' }] };
  ui = await startUI([], 0, false, { command: 'ui-chat', operator: true, history: open });
  await page.goto(ui.start);
  await page.locator('.conv-btn[data-key="dev/rechner__a"]').click();
  await expect(page.locator('#msgs article .tag.warn')).toHaveText('open');
  await page.getByRole('button', { name: 'Answer', exact: true }).click();
  await expect(page.locator('#replying-text')).toContainText('may I deploy?');
  await expect(page.locator('#kind')).toBeHidden();
  await page.locator('#replying-x').click();
  await expect(page.locator('#replying')).toBeHidden();
  await ui.stop();
  ui = await startUI(['--readonly'], 0, false, { command: 'ui-chat', operator: true, history: open });
  await page.goto(ui.start);
  await page.locator('.conv-btn[data-key="dev/rechner__a"]').click();
  await expect(page.locator('#msgs article')).toHaveCount(1);
  await expect(page.locator('#composer')).toBeHidden();
  await expect(page.getByRole('button', { name: 'Answer', exact: true })).toHaveCount(0);
});

test('files: a card with a download, a file can be chosen and checked, a group without an object store says why', async ({ page }) => {
  const withFile = { rechner__a: [{ n: 1, t: ago(5), dir: 'in', from: 'a', type: 'msg', text: 'the report', att_id: 'obj1', att_type: 'pdf', att_name: 'report.pdf', att_size: 2048, att_sha256: 'ab'.repeat(32) }] };
  ui = await startUI([], 0, false, { command: 'ui-chat', operator: true, history: withFile });
  await page.goto(ui.start);
  await page.locator('.conv-btn[data-key="dev/rechner__a"]').click();
  await expect(page.locator('.card .nm')).toHaveText('report.pdf');
  await expect(page.locator('.card .pv')).toContainText('pdf · 2 KiB · abababababab');
  const link = page.locator('.card a.dl');
  await expect(link).toHaveAttribute('href', '/api/chat/file?group=dev&conv=rechner__a&n=1');
  await expect(link).toHaveAttribute('download', 'report.pdf');
  await expect(page.locator('#attach')).toBeEnabled();
  await page.setInputFiles('#picker', { name: 'pic.png', mimeType: 'image/png', buffer: Buffer.from('89504e470d0a1a0a' + '07'.repeat(50), 'hex') });
  await expect(page.locator('#chip-text')).toContainText('pic.png');
  await expect(page.locator('#send')).toBeEnabled(); // a file alone is a message
  await page.press('#text', 'Control+Enter'); // no server of NATS: the file was looked at and taken, the sending fails and says so
  await expect(page.locator('#send-status')).toContainText('Not sent:');
  await expect(page.locator('#chip')).toBeVisible();
  await page.locator('#chip-x').click();
  await expect(page.locator('#chip')).toBeHidden();
  await page.setInputFiles('#picker', { name: 'big.zip', mimeType: 'application/zip', buffer: Buffer.alloc(9 << 20) });
  await expect(page.locator('#send-status')).toContainText('the group takes at most');
  await expect(page.locator('#chip')).toBeHidden();
  await ui.stop();
  ui = await startUI([], 0, false, { command: 'ui-chat', operator: true, objects: false });
  await page.goto(ui.start);
  await page.locator('.conv-btn[data-key="dev/rechner__a"]').click();
  await expect(page.locator('#attach')).toBeDisabled();
  await expect(page.locator('#attach')).toHaveAttribute('title', /no object store/);
});

test('unread counts name the entry and the tab, search finds a part of a word and opens the conversation, history can be deleted', async ({ page }) => {
  ui = await startUI([], 0, false, { command: 'ui-chat', operator: true, history });
  await page.goto(ui.start);
  await expect(page).toHaveTitle('(4) viper chat'); // 3 + 1: "all" is not counted twice, and the pair is only watched
  await expect(page.locator('.conv-btn[data-key="dev/rechner__a"]')).toHaveAttribute('aria-label', /^a, not seen yet, 3 unread$/);
  await page.fill('#q', 'NOBODY'); // below two characters nothing is asked; this is a part of a word in any case
  await expect(page.locator('#results .hit')).toHaveCount(0);
  await page.fill('#q', 'no setting');
  await expect(page.locator('#results .hit')).toHaveCount(1);
  await expect(page.locator('#results .hit .pv')).toContainText('b: topic that no setting lists');
  await expect(page.locator('#groups')).toBeHidden();
  await page.locator('#results .hit').click();
  await expect(page.locator('#conv-name')).toHaveText('#gone');
  await expect(page.locator('#results')).toBeHidden();
  await expect(page.locator('#msgs article')).toHaveCount(1);
  await page.locator('#del').click();
  await expect(page.locator('#confirm')).toContainText('cannot be fetched again');
  await page.locator('#confirm-x').click(); // cancelled: nothing happens
  await expect(page.locator('#msgs article')).toHaveCount(1);
  await page.locator('#del').click();
  await page.locator('#confirm-ok').click();
  await expect(page.locator('#msgs')).toContainText('No messages yet');
  await page.reload();
  await expect(page.locator('.conv-btn[data-key="dev/thema__gone"]')).toHaveCount(0); // it had only history
  await page.locator('.conv-btn[data-key="dev/rechner__a"]').click();
  await page.locator('#delall').click();
  await page.locator('#confirm-ok').click();
  await expect(page.locator('#msgs')).toContainText('No messages yet');
  await expect(page.locator('.conv-btn[data-key="dev/paar__a__b"] .pv')).toHaveCount(0); // the pair of two machines stays, empty
  await ui.stop();
  ui = await startUI(['--readonly'], 0, false, { command: 'ui-chat', operator: true, history });
  await page.goto(ui.start);
  await page.locator('.conv-btn[data-key="dev/rechner__a"]').click();
  await expect(page.locator('#del')).toBeHidden();
  await expect(page.locator('#delall')).toBeHidden();
});

test('keyboard: the arrow keys move in the list; narrow: the thread takes the place of the list and Back returns', async ({ browser }) => {
  ui = await startUI([], 0, false, { command: 'ui-chat', operator: true, history });
  const ctx = await browser.newContext({ viewport: { width: 390, height: 800 } });
  const page = await ctx.newPage();
  await page.goto(ui.start);
  await expect(page.locator('.conv-btn').first()).toBeVisible();
  const first = page.locator('.conv-btn').first();
  await first.focus();
  await page.keyboard.press('ArrowDown');
  await expect(page.locator('.conv-btn').nth(1)).toBeFocused();
  await page.keyboard.press('End');
  await expect(page.locator('.conv-btn').last()).toBeFocused();
  await page.keyboard.press('Home');
  await expect(first).toBeFocused();
  await page.locator('.conv-btn[data-key="dev/rechner__a"]').click();
  await expect(page.locator('#list-pane')).toBeHidden();
  await expect(page.locator('#msgs article')).toHaveCount(3);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true); // no sideways scrolling
  await page.locator('#back').click();
  await expect(page.locator('#list-pane')).toBeVisible();
  await expect(page.locator('.conv-btn[data-key="dev/rechner__a"]')).toBeFocused();
  await page.locator('.conv-btn[data-key="dev/alle"]').click();
  await expect(page.locator('#msgs')).toHaveAttribute('aria-live', 'off');
  await ctx.close();
});

test('the page stays still: the list is not built again every few seconds, and the line at the top does not come and go', async ({ page }) => {
  ui = await startUI([], 0, false, { command: 'ui-chat', operator: true, history, notStarted: true });
  await page.goto(ui.start);
  const btn = page.locator('.conv-btn[data-key="dev/rechner__a"]');
  await btn.focus();
  await btn.evaluate((e) => { (e as unknown as { mark: number }).mark = 1; });
  const top = (await page.locator('.shell').boundingBox())!.y;
  await page.waitForTimeout(11_000); // two snapshots of the server
  expect(await btn.evaluate((e) => (e as unknown as { mark?: number }).mark)).toBe(1); // the same element: nothing was built again
  await expect(btn).toBeFocused();
  expect((await page.locator('.shell').boundingBox())!.y).toBe(top);
  await page.evaluate(() => { document.getElementById('banner')!.hidden = true; });
  expect((await page.locator('.shell').boundingBox())!.y).toBe(top); // it goes: the page does not move
  await page.evaluate(() => { const b = document.getElementById('banner')!; b.textContent = 'a message that is much longer than the bar can show, '.repeat(8); b.hidden = false; });
  expect((await page.locator('.shell').boundingBox())!.y).toBe(top); // it comes, even a long one: the page does not move
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await expect(page.locator('.group h2 .led')).toHaveClass(/off/);
});

test('groups and their parts fold, and the choice is kept; pairs of few machines are there before they write', async ({ page }) => {
  ui = await startUI([], 0, false, { command: 'ui-chat', operator: true });
  await page.goto(ui.start);
  await expect(page.locator('.conv-btn[data-key="dev/paar__a__b"]')).toBeVisible(); // nothing written yet
  await expect(page.locator('.conv-btn[data-key="dev/rechner__a"]')).toBeVisible();
  const machines = page.locator('button.sec[data-fold="dev/Machines"]');
  await expect(machines).toHaveAttribute('aria-expanded', 'true');
  await machines.click();
  await expect(page.locator('.conv-btn[data-key="dev/rechner__a"]')).toHaveCount(0);
  await expect(machines).toHaveAttribute('aria-expanded', 'false');
  await expect(page.locator('.conv-btn[data-key="dev/paar__a__b"]')).toBeVisible(); // the other parts stay
  await page.reload();
  await expect(page.locator('.conv-btn[data-key="dev/rechner__a"]')).toHaveCount(0); // kept
  await page.locator('button.fold', { hasText: 'dev' }).click();
  await expect(page.locator('.conv-btn')).toHaveCount(0);
  await expect(page.locator('button.fold', { hasText: 'dev' })).toHaveAttribute('aria-expanded', 'false');
  await page.locator('button.fold', { hasText: 'dev' }).click();
  await expect(page.locator('.conv-btn[data-key="dev/alle"]')).toBeVisible();
});

test('a group that is not started takes nothing: the field says where the text goes and why it cannot', async ({ page }) => {
  ui = await startUI([], 0, false, { command: 'ui-chat', operator: true, notStarted: true });
  await page.goto(ui.start);
  await page.locator('.conv-btn[data-key="dev/rechner__a"]').click();
  await expect(page.locator('#target')).toContainText('The group dev is not started');
  await expect(page.locator('#text')).toBeDisabled();
  await expect(page.locator('#send')).toBeDisabled();
});

test('the field says to which machine in which group the text goes', async ({ page }) => {
  ui = await startUI([], 0, false, { command: 'ui-chat', operator: true });
  await page.goto(ui.start);
  await page.locator('.conv-btn[data-key="dev/rechner__a"]').click();
  await expect(page.locator('#target')).toHaveText('Sends to a in the group dev');
  await expect(page.locator('#text')).toBeEnabled(); // started: the field takes text
});

test('what a machine writes on a topic can be shown in its conversation, with the topic as a mark', async ({ page }) => {
  const h = {
    rechner__a: [{ n: 1, t: ago(9), dir: 'in', from: 'a', type: 'msg', text: 'direct to the operator' }],
    thema__general: [
      { n: 2, t: ago(8), dir: 'in', from: 'a', topic: 'general', type: 'msg', text: 'a on a topic' },
      { n: 3, t: ago(7), dir: 'in', from: 'b', topic: 'general', type: 'msg', text: 'b on a topic' },
    ],
  };
  ui = await startUI([], 0, false, { command: 'ui-chat', operator: true, history: h });
  await page.goto(ui.start);
  await page.locator('.conv-btn[data-key="dev/rechner__a"]').click();
  await expect(page.locator('#msgs article')).toHaveCount(1);
  await expect(page.locator('#withtopics')).not.toBeChecked(); // off
  await page.locator('#withtopics').check();
  await expect(page.locator('#msgs article')).toHaveCount(2); // a's own words on the topic, not those of b
  await expect(page.locator('#msgs article').nth(1)).toContainText('a on a topic');
  await expect(page.locator('#msgs article').nth(1).locator('.tag')).toHaveText('#general');
  await expect(page.locator('#msgs')).not.toContainText('b on a topic');
  await page.reload(); // kept
  await page.locator('.conv-btn[data-key="dev/rechner__a"]').click();
  await expect(page.locator('#withtopics')).toBeChecked();
  await expect(page.locator('#msgs article')).toHaveCount(2);
  await page.locator('.conv-btn[data-key="dev/thema__general"]').click();
  await expect(page.locator('#withtopics')).toBeHidden(); // only in the conversation of a machine
  await expect(page.locator('#msgs article')).toHaveCount(2); // both writers on the topic itself
});

test('a machine whose agent reads the text in its session: the first try asks, the second carries the OK', async ({ page }) => {
  ui = await startUI([], 0, false, { command: 'ui-chat', operator: true, push: true });
  await page.goto(ui.start);
  await page.locator('.conv-btn[data-key="dev/rechner__a"]').click();
  const bodies: { ack: string[]; text: string; group: string; to: string }[] = [];
  page.on('request', (r) => { if (r.url().endsWith('/api/chat/send')) { bodies.push(JSON.parse(r.postData() ?? '{}')); } });
  await page.fill('#text', 'Hallo');
  await page.press('#text', 'Control+Enter');
  await expect(page.locator('#ack')).toBeVisible(); // the question, with the machines and the group
  await expect(page.locator('#ack-text')).toContainText('a');
  await expect(page.locator('#ack-text')).toContainText('(dev)');
  await expect(page.locator('#text')).toHaveValue('Hallo'); // nothing was sent, the text stays
  expect(bodies).toHaveLength(1);
  expect(bodies[0].ack).toEqual([]);
  await page.locator('#ack-ok').click();
  await expect(page.locator('#send-status')).toContainText('Not sent:'); // no server of NATS here: the second try went out, and failed there
  expect(bodies).toHaveLength(2);
  expect(bodies[1]).toMatchObject({ group: 'dev', to: 'a', text: 'Hallo', ack: ['a'] });
  await expect(page.locator('#ack')).toBeHidden();
});

test('the notification is off, and a choice to turn it on is kept', async ({ browser, browserName }) => {
  test.skip(browserName === 'webkit', 'WebKit does not let a test grant the permission for notifications; without it the switch stays off, as it should');
  const ctx = await browser.newContext();
  await ctx.grantPermissions(['notifications']);
  const page = await ctx.newPage();
  ui = await startUI([], 0, false, { command: 'ui-chat', operator: true });
  await page.goto(ui.start);
  await expect(page.locator('#notify')).not.toBeChecked();
  await page.locator('#notify').check();
  await page.reload();
  await expect(page.locator('#notify')).toBeChecked();
  await page.locator('#notify').uncheck();
  await page.reload();
  await expect(page.locator('#notify')).not.toBeChecked();
  await ctx.close();
});

test('the language can be changed and the texts follow', async ({ page }) => {
  ui = await startUI([], 0, false, { command: 'ui-chat' });
  await page.goto(ui.start);
  await page.selectOption('#lang', 'de');
  await expect(page.locator('#me')).toContainText('nicht angemeldet');
  await expect(page.locator('#empty h2')).toHaveText('Sie sind in dieser Gruppe nicht angemeldet');
});

test('the chat has no route of viper ui: it cannot start a run', async ({ page }) => {
  ui = await startUI([], 0, false, { command: 'ui-chat', operator: true });
  await page.goto(ui.start);
  await expect(page.locator('#me')).toContainText('logged in'); // the start page has moved on to the chat
  const codes = await page.evaluate(async () => {
    const out: number[] = [];
    for (const p of ['/api/run', '/api/check', '/api/edit/apply']) {
      const r = await fetch(p, { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json', 'X-Viper-UI': '1' }, body: '{"action":"down","machine":"web"}' });
      out.push(r.status);
    }
    out.push((await fetch('/api/overview', { credentials: 'same-origin' })).status);
    return out;
  });
  expect(codes.every((c) => c >= 400)).toBe(true);
});

test('a second viper ui-chat says that one runs already, and with which port', async () => {
  ui = await startUI([], 0, false, { command: 'ui-chat' });
  const { spawnSync } = await import('node:child_process');
  const { envWith } = await import('../fixture');
  const r = spawnSync(process.env.VIPER_BIN as string, ['ui-chat', '--no-open'], { cwd: ui.work, env: envWith({ HOME: ui.home, USERPROFILE: ui.home }, ''), encoding: 'utf8', timeout: 20_000 });
  expect(r.status).not.toBe(0);
  expect(r.stderr).toMatch(/it runs already \(port \d+\)/);
});

for (const scheme of ['light', 'dark'] as const) {
  for (const lang of ['en', 'de']) {
    test(`the chat has no accessibility violations (${scheme}, ${lang})`, async ({ browser }) => {
      ui = await startUI([], 0, false, { command: 'ui-chat', operator: true, history });
      const ctx = await browser.newContext({ colorScheme: scheme, locale: lang });
      const page = await ctx.newPage();
      await page.goto(ui.start);
      await expect(page.locator('.conv-btn')).toHaveCount(5);
      await page.locator('.conv-btn[data-key="dev/rechner__a"]').click();
      await expect(page.locator('#msgs article')).toHaveCount(3);
      const result = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'best-practice']).analyze();
      expect(result.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(' ')).join(', ')}`), `${scheme} ${lang}`).toEqual([]);
      await ctx.close();
    });
  }
}
