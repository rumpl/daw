import { defineConfig, devices } from '@playwright/test';
import { homedir } from 'node:os';
import { join } from 'node:path';

// Chromium only: it is the single browser the suite needs, so `playwright
// install chromium` is the only download required.
//
// The suite always runs against the *production binary* with the deterministic
// fake docker-agent adapter, so it never spends model tokens or starts a Docker
// sandbox VM.
const PORT = process.env.E2E_PORT ?? '4799';

export default defineConfig({
  testDir: './tests',
  fullyParallel: false,
  workers: 1,
  timeout: 45_000,
  expect: { timeout: 10_000 },
  reporter: process.env.CI ? 'line' : [['list']],
  use: {
    baseURL: `http://127.0.0.1:${PORT}`,
    trace: 'retain-on-failure',
  },
  projects: [
    { name: 'desktop', use: { ...devices['Desktop Chrome'], viewport: { width: 1280, height: 900 } } },
    { name: 'mobile', use: { ...devices['Pixel 5'], viewport: { width: 390, height: 844 } } },
  ],
  webServer: {
    command: '../bin/dawui',
    cwd: '.',
    url: `http://127.0.0.1:${PORT}/api/health`,
    reuseExistingServer: false,
    stdout: 'ignore',
    stderr: 'pipe',
    env: {
      PORT,
      DAWUI_FAKE_ADAPTER: '1',
      DAWUI_FAKE_DELAY_MS: '120',
      DAWUI_PLUGIN_DIR: process.env.E2E_PLUGIN_DIR ?? join(homedir(), '.dawui-e2e', 'plugins'),
    },
  },
});
