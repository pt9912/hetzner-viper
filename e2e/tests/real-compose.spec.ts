import { execFileSync } from 'node:child_process';
import { expect, test } from '@playwright/test';
import { startRealCompose } from '../fixture-real';

// Real Docker: preflight, up, check and down of one machine through the pages. By hand: E2E_REAL_COMPOSE=1, and
// E2E_COMPOSE_IMAGE=<an image of the machines that Docker has>. It makes one container and its volumes, named m5t-web...,
// and deletes them.
test.skip(!process.env.E2E_REAL_COMPOSE, 'needs Docker and an image of the machines (E2E_REAL_COMPOSE=1)');
test.setTimeout(20 * 60_000);

const mine = () => execFileSync('docker', ['ps', '-a', '--filter', 'name=m5t-web', '--format', '{{.Names}}'], { encoding: 'utf8' }).trim();

test('a machine on real Docker: preflight, up, check, down with the question typed in the page', async ({ page }) => {
  const ui = await startRealCompose(process.env.E2E_COMPOSE_IMAGE || 'viper-machine:test');
  try {
    expect(mine()).toBe('');
    await page.goto(ui.start);
    await expect(page.locator('#machines tbody tr')).toHaveCount(1);

    await page.getByRole('button', { name: 'Preflight web', exact: true }).click();
    await expect(page.locator('#run-status')).toContainText('Done: exit 0', { timeout: 120_000 });
    await expect(page.locator('#run-output')).toContainText('Preflight');

    await page.click('#tab-overview');
    await page.getByRole('button', { name: 'Up web', exact: true }).click();
    await expect(page.locator('#run-output')).toContainText('$ viper up web');
    await expect(page.locator('#run-status')).toContainText('Done: exit 0', { timeout: 15 * 60_000 });
    expect(mine()).toContain('m5t-web');

    await page.click('#tab-overview');
    await expect(page.locator('#machines tbody tr', { hasText: 'web' })).toContainText('set up');
    await page.getByRole('button', { name: 'Check web', exact: true }).click();
    await expect(page.locator('#check-status')).toContainText('web: OK', { timeout: 5 * 60_000 });

    await page.getByRole('button', { name: 'Down web', exact: true }).click();
    await expect(page.locator('#run-output')).toContainText('$ viper down web');
    await expect(page.locator('#run-status')).toContainText('Waiting for your answer', { timeout: 5 * 60_000 });
    await expect(page.locator('#run-output')).toContainText('DELETE');
    await page.fill('#run-answer', 'DELETE');
    await page.press('#run-answer', 'Enter');
    await expect(page.locator('#run-status')).toContainText('Done: exit 0', { timeout: 5 * 60_000 });
    expect(mine()).toBe('');
    await page.click('#tab-overview');
    await expect(page.locator('#machines tbody tr', { hasText: 'web' })).toContainText('not started');
  } finally {
    await ui.stop();
  }
});
