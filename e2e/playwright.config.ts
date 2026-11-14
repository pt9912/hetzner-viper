import { defineConfig } from '@playwright/test';

// Browser tests of viper ui. They start the real program on a free port, in a directory of their own, with a fake docker
// (so that nothing is made anywhere); see fixture.ts. E2E_BROWSERS=chromium,firefox,webkit chooses the browsers (default: chromium).
// In a container the browser needs a bigger /dev/shm or none (E2E_CONTAINER=1 sets the flags of that case).
const chromiumArgs = process.env.E2E_CONTAINER ? ['--no-sandbox', '--disable-dev-shm-usage', '--disable-gpu'] : [];
const wanted = (process.env.E2E_BROWSERS || 'chromium').split(',').map((s) => s.trim());

const all = {
  chromium: { name: 'chromium', use: { browserName: 'chromium' as const, channel: 'chromium', launchOptions: { args: chromiumArgs } } },
  firefox: { name: 'firefox', use: { browserName: 'firefox' as const } },
  webkit: { name: 'webkit', use: { browserName: 'webkit' as const } },
};

export default defineConfig({
  testDir: './tests',
  globalSetup: './global-setup.ts',
  workers: 1,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  reporter: [['list']],
  use: { headless: true, trace: 'retain-on-failure' },
  projects: wanted.map((w) => all[w as keyof typeof all]).filter(Boolean),
});
