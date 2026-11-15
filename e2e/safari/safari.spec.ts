import { expect, test } from '@playwright/test';
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
  ui = await startUI([], 30, withFile);
  driver = await new Builder().forBrowser(Browser.SAFARI).build();
});
test.afterEach(async () => {
  try { await driver.quit(); } catch { /* gone */ }
  await ui.stop();
});

async function rows(): Promise<number> { return (await driver.findElements(By.css('#machines tbody tr'))).length; }
async function pageText(): Promise<string> { try { return (await driver.findElement(By.css('body')).getText()).slice(0, 300); } catch { return '(no body)'; } }

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
    console.log(`SAFARI start file ${file}: url = ${url}; text = ${JSON.stringify(await pageText())}`);
    if (!url.startsWith('safari-resource:') && !url.startsWith('about:')) { opened = file; break; }
  }
  expect(opened, 'Safari could not open any start file').not.toBe('');
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


test.describe.configure({ mode: 'serial' });
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
