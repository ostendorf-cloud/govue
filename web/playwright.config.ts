// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { mkdirSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import { defineConfig, devices } from '@playwright/test'

// The Go example server of the repository, which the contact spec runs
// against. The Vite harness proxies /api to it, so the page and the API share
// one origin, as in a real site. Run by hand with the same environment:
//
//   cd <repository root> && go run ./example
//
// The keys are built at run time from short repeated hex strings, never
// written as long literals. They only live for this run, in a temporary
// directory, and sign nothing of value.
const GO_ADDR = '127.0.0.1:18080'
const keyDir = join(tmpdir(), 'govue-e2e-contact')
mkdirSync(keyDir, { recursive: true, mode: 0o700 })
const sessionKeyFile = join(keyDir, 'session.hex')
const csrfKeyFile = join(keyDir, 'csrf.hex')
writeFileSync(sessionKeyFile, '1a2b'.repeat(16), { mode: 0o600 })
writeFileSync(csrfKeyFile, '5e6f'.repeat(16), { mode: 0o600 })

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
  webServer: [
    {
      command: 'pnpm exec vite --config e2e/harness/vite.config.ts',
      url: 'http://localhost:4173',
      reuseExistingServer: !process.env.CI,
    },
    {
      // The example wires /api/v1/contact and /api/v1/contact/challenge with a
      // fake sender. The proof of work, the limits and the answers are the real
      // handlers'. Its limits are per process: 5 submissions per 10 minutes.
      command: 'go run ./example',
      cwd: '..',
      url: `http://${GO_ADDR}/api/v1/contact/challenge`,
      timeout: 240_000,
      reuseExistingServer: !process.env.CI,
      env: {
        ADDR: GO_ADDR,
        ADMIN_ADDR: '127.0.0.1:18081',
        ALLOW_PLAIN_HTTP: 'true',
        SESSION_KEY: sessionKeyFile,
        CSRF_KEY: csrfKeyFile,
        CONTACT_POW_KEY: '0a1b'.repeat(16),
        OIDC_ISSUER: 'https://issuer.example.test',
        OIDC_CLIENT_ID: 'e2e',
        OIDC_CLIENT_SECRET: 'e2e-not-a-secret',
        OIDC_REDIRECT_URL: 'http://localhost:4173/callback',
        RATE_LIMIT_BURST: '1000',
      },
    },
  ],
})
