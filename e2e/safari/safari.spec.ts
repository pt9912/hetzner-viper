import { expect, test } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import { pathToFileURL } from 'node:url';
import { Browser, Builder, By, Key, until, WebDriver } from 'selenium-webdriver';
import { startUI, UI } from '../fixture';

// The real Safari (safaridriver, a Mac with "Allow Remote Automation": `sudo safaridriver --enable`). Playwright's WebKit is not
// Safari: what is different there (the start file, the Tab key) is looked at here. By hand: E2E_BROWSERS=safari npx playwright test.
test.skip(process.platform !== 'darwin', 'Safari is on a Mac');
test.setTimeout(120_000);

let ui: UI;
let driver: WebDriver;

test.beforeEach(async () => {
  const withFile = test.info().title.startsWith('the start file'); // the first test needs the start file, the others the address with the key
  ui = await startUI([], 0, withFile); // a docker that answers at once: a preflight ends with problems after a moment
  driver = await new Builder().forBrowser(Browser.SAFARI).build();
});
test.afterEach(async () => {
  try { await driver.quit(); } catch { /* gone */ }
  await ui.stop();
});

async function rows(): Promise<number> { return (await driver.findElements(By.css('#machines tbody tr'))).length; }
async function pageText(): Promise<string> { try { return (await driver.findElement(By.css('body')).getText()).slice(0, 900); } catch { return '(no body)'; } }

test('the start file leads to the page in Safari (by itself, or by its link), and the page works', async () => {
  // where the file is matters to a sandboxed Safari: try the temp folder (with its real path) and then a folder of the home
  const launch = new URL(ui.start).pathname;
  const candidates = [fs.realpathSync(launch), path.join(os.homedir(), 'viper-e2e-launch.html')];
  fs.copyFileSync(launch, candidates[1]);
  let opened = '';
  for (const file of candidates) {
    await driver.get(pathToFileURL(file).href);
    await driver.sleep(1500);
    const url = await driver.getCurrentUrl();
    console.log(`SAFARI start file ${file}: url = ${url}; text = ${JSON.stringify((await pageText()).replace(/\s+/g, ' '))}`);
    if (!url.startsWith('safari-resource:') && !url.startsWith('about:')) { opened = file; break; }
  }
  // the Safari that safaridriver starts may refuse local files altogether (not what a person has): then the test of `open` below decides
  test.skip(opened === '', 'the automated Safari does not open local files (see the log); the start file is tried with `open` in the next test');
  // does Safari follow the refresh of a file:// page to http://127.0.0.1 by itself?
  let automatic = false;
  try { await driver.wait(async () => (await driver.getCurrentUrl()).startsWith(ui.base), 6000); automatic = true; } catch { /* not by itself */ }
  console.log(`SAFARI start file: automatic redirect = ${automatic}; url = ${await driver.getCurrentUrl()}`);
  if (!automatic) {
    await driver.findElement(By.linkText('viper ui')).click();
    await driver.wait(until.urlIs(ui.base + '/'), 10000);
  }
  await driver.wait(async () => (await rows()) === 3, 10000);
  expect(await driver.getTitle()).toBe('viper');
  expect(await driver.getPageSource()).not.toContain('ghp_e2esecretvalue123');
  fs.rmSync(candidates[1], { force: true });
});


test('Safari: the language, a refused page, a run with its exit status', async () => {
  await driver.get(ui.start);
  await driver.wait(until.urlIs(ui.base + '/'), 15000).catch(async () => { await driver.findElement(By.linkText('viper ui')).click(); await driver.wait(until.urlIs(ui.base + '/'), 10000); });
  await driver.wait(async () => (await rows()) === 3, 10000);
  // the language
  await driver.findElement(By.css('#lang option[value="de"]')).click();
  await driver.wait(until.elementTextIs(driver.findElement(By.css('#tab-settings')), 'Einstellungen'), 5000);
  await driver.findElement(By.css('#lang option[value="en"]')).click();
  await driver.wait(until.elementTextIs(driver.findElement(By.css('#tab-settings')), 'Settings'), 5000);
  // a preflight against the fake docker: it ends with problems, as in the other browsers
  await driver.findElement(By.css('button[aria-label="Preflight web"]')).click();
  const status = driver.findElement(By.id('run-status'));
  await driver.wait(until.elementTextContains(status, 'Ended: exit 1'), 60000);
  // the session cookie is for this page only: a second browser session without it is refused
  const bare = await new Builder().forBrowser(Browser.SAFARI).build().catch(() => null);
  if (bare) {
    try {
      await bare.get(ui.base + '/api/overview');
      expect(await bare.getPageSource()).toContain('forbidden');
    } finally { await bare.quit().catch(() => {}); }
  }
});

test('Safari: the arrow keys switch the tabs', async () => {
  await driver.get(ui.start);
  await driver.wait(until.urlIs(ui.base + '/'), 15000).catch(async () => { await driver.findElement(By.linkText('viper ui')).click(); await driver.wait(until.urlIs(ui.base + '/'), 10000); });
  await driver.wait(async () => (await rows()) === 3, 10000);
  const tab = driver.findElement(By.id('tab-overview'));
  await tab.click();
  await tab.sendKeys(Key.ARROW_RIGHT);
  await driver.wait(until.elementIsVisible(driver.findElement(By.id('page-runs'))), 5000);
  expect(await driver.findElement(By.id('tab-runs')).getAttribute('aria-selected')).toBe('true');
});

// What a person gets: viper ui opens the start file with the program of the system (open), in the Safari of the user. The tab is
// looked at with AppleScript (it may need the permission of the system: then the log says so, and nothing else is claimed).
test.describe('opened with open', () => {
  test('the start file, opened with `open -a Safari`: where does Safari end?', async () => {
    const launch = new URL(ui.start).pathname;
    const home = path.join(os.homedir(), 'viper-e2e-open.html');
    fs.copyFileSync(launch, home);
    const sh = (cmd: string[]) => { try { return execFileSync(cmd[0], cmd.slice(1), { encoding: 'utf8', timeout: 20000 }).trim(); } catch (e) { return 'ERROR ' + String(e).slice(0, 300); } };
    sh(['open', '-a', 'Safari', home]);
    let url = '';
    for (let i = 0; i < 12 && !url.startsWith(ui.base); i++) {
      await new Promise((r) => setTimeout(r, 1000));
      url = sh(['osascript', '-e', 'tell application "Safari" to get URL of front document']);
    }
    const title = sh(['osascript', '-e', 'tell application "Safari" to get name of front document']);
    console.log(`SAFARI open: url = ${url}; title = ${title}; followed by itself = ${url.startsWith(ui.base)}`);
    sh(['osascript', '-e', 'tell application "Safari" to quit']);
    fs.rmSync(home, { force: true });
  });
});
