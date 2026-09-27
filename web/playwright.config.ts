import { mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import { defineConfig, devices } from '@playwright/test'

import { gitopsDir, secondPort, secondURL, sqliteDir } from './e2e/paths'

// E2E runs against the real binary built by `make build`, with a throwaway SQLite store.
const port = Number(process.env.ROWBIRD_E2E_PORT ?? 18080)
const dataDir = mkdtempSync(join(tmpdir(), 'rowbird-e2e-'))
// A second instance receives imports and applies a GitOps directory.
const secondDataDir = mkdtempSync(join(tmpdir(), 'rowbird-e2e-second-'))

export default defineConfig({
  testDir: './e2e',
  globalSetup: './e2e/global-setup.ts',
  // The journey shares one server and builds on earlier steps.
  workers: 1,
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? 'github' : 'list',
  use: {
    baseURL: `http://127.0.0.1:${port}`,
    trace: 'retain-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: [{
    command: '../bin/rowbird serve',
    url: `http://127.0.0.1:${port}/health/ready`,
    reuseExistingServer: false,
    timeout: 30_000,
    env: {
      ROWBIRD_DATA_DIR: dataDir,
      ROWBIRD_LISTEN_ADDR: `127.0.0.1:${port}`,
      ROWBIRD_BASE_URL: `http://127.0.0.1:${port}`,
      ROWBIRD_LOG_FORMAT: 'json',
      ROWBIRD_UPDATE_CHECK: 'false',
      ROWBIRD_SQLITE_DIRS: sqliteDir,
    },
  }, {
    command: '../bin/rowbird serve',
    url: `http://127.0.0.1:${secondPort}/health/ready`,
    reuseExistingServer: false,
    timeout: 30_000,
    env: {
      ROWBIRD_DATA_DIR: secondDataDir,
      ROWBIRD_LISTEN_ADDR: `127.0.0.1:${secondPort}`,
      ROWBIRD_BASE_URL: secondURL,
      ROWBIRD_LOG_FORMAT: 'json',
      ROWBIRD_UPDATE_CHECK: 'false',
      ROWBIRD_LOG_LEVEL: 'warn',
      ROWBIRD_SQLITE_DIRS: sqliteDir,
      ROWBIRD_CONFIG_DIR: gitopsDir,
    },
  }],
})
