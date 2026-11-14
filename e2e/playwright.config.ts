import { defineConfig } from '@playwright/test';

// Browser tests of viper ui. They start the real program on a free port, in a directory of their own, with a fake docker
// (so that nothing is made anywhere); see fixture.ts.
// In a container the browser needs a bigger /dev/shm or none (E2E_CONTAINER=1 sets the flags of that case).
const args = process.env.E2E_CONTAINER ? ['--no-sandbox', '--disable-dev-shm-usage', '--disable-gpu'] : [];

export default defineConfig({
  testDir: './tests',
  globalSetup: './global-setup.ts',
  workers: 1,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  reporter: [['list']],
  use: { browserName: 'chromium', channel: 'chromium', headless: true, launchOptions: { args }, trace: 'retain-on-failure' },
});
