import { expect, test } from '@playwright/test';
import { startUI, UI } from '../fixture';

// the page of the build of an image: what the page starts, the real viper takes
let ui: UI;
test.afterEach(async () => { await ui.stop(); });

test('the name and the cluster go to viper image build, and its output is in the area of the page', async ({ page }) => {
  test.setTimeout(120_000);
  ui = await startUI([], 0);
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit')).toHaveCount(3);
  await page.click('#tab-image');
  await expect(page.locator('#image-go')).toBeEnabled();
  await page.fill('#i-tag', 'viper-test:v1');
  await page.click('#image-go');
  await expect(page.locator('#image-out')).toContainText('$ viper image build --tag=viper-test:v1', { timeout: 30_000 });
  // the run ends (without Docker or with it: the build of a test image is not what is tested here); the page is free again
  await expect(page.locator('#image-go')).toBeEnabled({ timeout: 90_000 });
  const cancel = page.locator('#image-stop');
  await expect(cancel).toBeDisabled();
});

test('a name that is no image name is refused and nothing is built; a page that only reads does not offer it', async ({ page }) => {
  ui = await startUI([], 0);
  await page.goto(ui.start);
  await page.click('#tab-image');
  await page.fill('#i-tag', 'Upper Case');
  await page.click('#image-go');
  await expect(page.locator('#image-status')).toContainText('refused');
  await ui.stop();
  ui = await startUI(['--readonly'], 0);
  await page.goto(ui.start);
  await page.click('#tab-image');
  await expect(page.locator('#image-go')).toBeDisabled();
  await expect(page.locator('#image-status')).toContainText('read only');
});

test('Cancel ends the build: the program that viper started is ended as well, not only viper', async ({ page }) => {
  test.skip(process.platform === 'win32', 'the interrupt of a program works differently on Windows');
  test.setTimeout(120_000);
  ui = await startUI([], 60); // the fake docker waits 60 seconds
  await page.goto(ui.start);
  await expect(page.locator('#machines tr.unit')).toHaveCount(3);
  await page.click('#tab-image');
  await page.click('#image-go');
  await expect(page.locator('#image-out')).toContainText('fake docker build', { timeout: 30_000 });
  await page.click('#image-stop');
  // viper is interrupted at once and ends the fake docker; without that the run is killed only after 20 seconds
  await expect(page.locator('#image-status')).toContainText('Cancelled', { timeout: 12_000 });
  await expect(page.locator('#image-go')).toBeEnabled();
});
