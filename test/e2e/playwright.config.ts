// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
  testDir: './tests',
  // The flows share one database and build on each other, so they run in order.
  workers: 1,
  // Waiting for the next TOTP time step takes up to 30 seconds.
  timeout: 90_000,
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: 0,
  reporter: [['list']],
  outputDir: './test-results',
  globalSetup: './global-setup.ts',
  globalTeardown: './global-teardown.ts',
  use: {
    baseURL: 'http://localhost:18080',
    trace: 'retain-on-failure',
    // The fake GitLab is reached by its compose host name, exactly as the
    // server reaches it, so the issuer URL is the same on both sides.
    launchOptions: {
      args: ['--host-resolver-rules=MAP fake-gitlab 127.0.0.1'],
    },
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
});
