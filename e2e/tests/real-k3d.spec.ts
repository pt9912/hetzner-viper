import { execFileSync } from 'node:child_process';
import { expect, test } from '@playwright/test';
import { k3dCluster, k3dRegistry, startRealK3d } from '../fixture-real';

// A real k3d cluster: preflight, up (which makes the registry and the cluster), check, down (the question that names the
// cluster, then the question about the registry) through the pages. By hand: E2E_REAL_K3D=1 and E2E_K3D_IMAGE=<an image of
// the machines that Docker has>. It makes the cluster m5k and the registry k3d-m5k-registry, and deletes both.
test.skip(!process.env.E2E_REAL_K3D, 'needs Docker, k3d and an image of the machines (E2E_REAL_K3D=1)');
test.setTimeout(40 * 60_000);

const k3d = (...a: string[]) => execFileSync('k3d', a, { encoding: 'utf8' });
const clusters = () => k3d('cluster', 'list', '-o', 'json').includes(`"name": "${k3dCluster}"`) || k3d('cluster', 'list').split('\n').some((l) => l.startsWith(k3dCluster + ' '));
const registries = () => execFileSync('docker', ['ps', '-a', '--filter', `name=${k3dRegistry}`, '--format', '{{.Names}}'], { encoding: 'utf8' }).trim();
const containers = () => execFileSync('docker', ['ps', '-a', '--filter', `name=k3d-${k3dCluster}`, '--format', '{{.Names}}'], { encoding: 'utf8' }).trim();

test('a machine in a k3d cluster that viper makes: up, check and down with both questions typed in the page', async ({ page }) => {
  const ui = await startRealK3d(process.env.E2E_K3D_IMAGE || 'viper-machine:test');
  try {
    expect(clusters()).toBeFalsy();
    expect(registries()).toBe('');
    await page.goto(ui.start);
    await expect(page.locator('#machines tr.unit')).toHaveCount(1);
    await expect(page.locator('#machines tr.unit', { hasText: 'web' })).toContainText('kubernetes');

    await page.getByRole('button', { name: 'Preflight web', exact: true }).click();
    await expect(page.locator('#run-output')).toContainText('$ viper preflight web');
    await expect(page.locator('#run-status')).toContainText('Done: exit 0', { timeout: 3 * 60_000 });
    await expect(page.locator('#run-output')).toContainText('k3d');
    expect(clusters()).toBeFalsy(); // a preflight makes nothing

    await page.click('#tab-overview');
    await page.getByRole('button', { name: 'Up web', exact: true }).click();
    await expect(page.locator('#run-output')).toContainText('$ viper up web');
    await expect(page.locator('#run-status')).toContainText('Done: exit 0', { timeout: 30 * 60_000 });
    expect(clusters()).toBeTruthy();
    expect(registries()).toContain(k3dRegistry);

    await page.click('#tab-overview');
    await expect(page.locator('#machines tr.unit', { hasText: 'web' })).toContainText('set up');
    await page.getByRole('button', { name: 'Check web', exact: true }).click();
    await expect(page.locator('#check-status')).toContainText('web: OK', { timeout: 5 * 60_000 });

    await page.getByRole('button', { name: 'Down web', exact: true }).click();
    await expect(page.locator('#run-output')).toContainText('$ viper down web');
    await expect(page.locator('#run-status')).toContainText('Waiting for your answer', { timeout: 5 * 60_000 });
    await expect(page.locator('#run-output')).toContainText('cluster'); // the question names the cluster that goes with the last machine
    await page.fill('#run-answer', 'DELETE');
    await page.press('#run-answer', 'Enter');
    await expect(page.locator('#run-status')).toContainText('Waiting for your answer', { timeout: 5 * 60_000 }); // the question about the registry
    await expect(page.locator('#run-output')).toContainText('registry');
    await page.fill('#run-answer', 'DELETE');
    await page.press('#run-answer', 'Enter');
    await expect(page.locator('#run-status')).toContainText('Done: exit 0', { timeout: 10 * 60_000 });
    expect(clusters()).toBeFalsy();
    expect(registries()).toBe('');
    expect(containers()).toBe('');
  } finally {
    await ui.stop();
    // what the test may have left, deleted by its own names
    if (clusters()) { try { k3d('cluster', 'delete', k3dCluster); } catch { /* gone */ } }
    if (registries()) { try { k3d('registry', 'delete', k3dRegistry); } catch { /* gone */ } }
  }
});
