// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { defineConfig, devices } from '@playwright/test'

// Chromium only: a real browser's focus trap, inert background and Escape
// handling do not vary enough across engines to justify a Firefox or WebKit
// project here, and this suite is not visual regression testing.
export default defineConfig({
  testDir: './e2e',
  fullyParallel: true,
  use: {
    baseURL: 'http://localhost:4173',
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
  webServer: {
    command: 'pnpm exec vite --config e2e/harness/vite.config.ts',
    url: 'http://localhost:4173',
    reuseExistingServer: !process.env.CI,
  },
})
