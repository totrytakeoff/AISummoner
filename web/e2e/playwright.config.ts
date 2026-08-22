import { defineConfig } from '@playwright/test'

import { loadPublicConfiguration } from './helpers/environment.js'

const publicConfiguration = loadPublicConfiguration()

export default defineConfig({
  testDir: './tests',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  forbidOnly: true,
  timeout: 20 * 60_000,
  globalTimeout: 30 * 60_000,
  expect: { timeout: 15_000 },
  reporter: [['line']],
  outputDir: '.playwright-output',
  preserveOutput: 'never',
  use: {
    baseURL: publicConfiguration.baseURL,
    browserName: 'chromium',
    headless: true,
    ignoreHTTPSErrors: publicConfiguration.allowScopedTLSErrors,
    screenshot: 'off',
    video: 'off',
    trace: 'off',
    acceptDownloads: false,
    serviceWorkers: 'block',
    locale: 'en-US',
    timezoneId: 'UTC',
  },
})
