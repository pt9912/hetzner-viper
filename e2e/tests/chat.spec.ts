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
  ui = await startUI([], 0, false, { command: 'ui-chat', operator: true });
  await page.goto(ui.start);
  await expect(page.locator('#me')).toContainText('Operator operator logged in');
  await expect(page.locator('.group h2')).toHaveText('dev');
  await expect(page.locator('.conv-btn')).toHaveCount(3);
  await page.getByRole('button', { name: 'a', exact: true }).click();
  await expect(page.locator('#conv-name')).toHaveText('a');
  await expect(page.locator('#conv-sub')).toContainText('Machine of the group dev');
  await expect(page.getByRole('button', { name: 'a', exact: true })).toHaveAttribute('aria-current', 'true');
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
