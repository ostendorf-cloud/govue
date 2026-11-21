// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// The contact form in a real Chromium, against the real Go handlers.
//
// playwright.config.ts starts two servers: the Vite harness on :4173 and the
// Go example server (`go run ./example` in the repository root, with
// CONTACT_POW_KEY set and a fake sender) on :18080. The harness proxies /api to
// it, so the page and the API share one origin. Run the suite with:
//
//   task web:e2e
//
// or, with Chromium installed, `pnpm exec playwright test e2e/contact.spec.ts`
// inside web/. Nothing here is mocked: the challenge, the proof of work, the
// limits, the validation and every answer are the server's. The one exception
// is the failed send, where the browser's request is aborted before it leaves.
//
// The server's limits are per process: a client may submit 5 times per 10
// minutes, and this file submits 3 times, so the suite runs once per server
// start. Restart the server to run it again.
import { expect, test, type Page } from '@playwright/test'

const ORIGIN = 'http://localhost:4173'

test.describe.configure({ mode: 'serial' })

interface Seen {
  foreign: string[]
  bodies: Array<Record<string, unknown>>
}

function watch(page: Page): Seen {
  const seen: Seen = { foreign: [], bodies: [] }
  page.on('request', (request) => {
    const url = new URL(request.url())
    if (url.protocol.startsWith('http') && url.origin !== ORIGIN) {
      seen.foreign.push(request.url())
    }
    if (request.method() === 'POST' && url.pathname === '/api/v1/contact') {
      seen.bodies.push(request.postDataJSON() as Record<string, unknown>)
    }
  })
  return seen
}

// Role locators with an anchored name: a label text match is a substring
// match, and the consent label contains the word "message".
const nameField = (page: Page) => page.getByRole('textbox', { name: /^Name\b/ })
const emailField = (page: Page) => page.getByRole('textbox', { name: /^Email address\b/ })
const messageField = (page: Page) => page.getByRole('textbox', { name: /^Message\b/ })

async function fill(page: Page, email = 'alice@example.com') {
  await nameField(page).fill('Alice')
  await emailField(page).fill(email)
  await messageField(page).fill('Hello from the browser')
  await page.getByRole('checkbox').check()
}

test('solves the challenge in a worker, sends it and shows success', async ({ page }) => {
  const seen = watch(page)
  await page.goto('/contact.html')
  await fill(page)

  await page.getByRole('button', { name: 'Send message' }).click()

  await expect(page.getByRole('status')).toContainText('Thank you. Your message was sent.', {
    timeout: 60_000,
  })
  await expect(page.getByRole('status')).toBeFocused()
  expect(seen.bodies).toHaveLength(1)
  // consent stays in the browser: the server refuses an unknown field.
  expect(Object.keys(seen.bodies[0]).sort()).toEqual(['email', 'message', 'name', 'pow', 'website'])
  expect(seen.foreign).toEqual([])
  expect(await page.evaluate(() => document.cookie)).toBe('')
})

test('loads the worker file from the same origin as a module worker', async ({ page }) => {
  const workers: string[] = []
  page.on('worker', (worker) => workers.push(worker.url()))
  await page.goto('/contact.html')

  await nameField(page).focus()

  await expect.poll(() => workers.length, { timeout: 30_000 }).toBeGreaterThan(0)
  expect(new URL(workers[0]).origin).toBe(ORIGIN)
  expect(workers[0]).toContain('pow.worker')
})

test('puts a field code of the server on its field and moves focus there', async ({ page }) => {
  await page.goto('/contact.html')
  await fill(page, 'not-an-address')

  await page.getByRole('button', { name: 'Send message' }).click()

  const email = emailField(page)
  await expect(email).toHaveAttribute('aria-invalid', 'true', { timeout: 60_000 })
  await expect(email).toHaveAccessibleDescription('This is not valid.')
  await expect(email).toBeFocused()
  await expect(page.getByRole('status')).not.toContainText('Thank you')
})

test('a send that never reaches the server is a failure, and the next send works', async ({
  page,
}) => {
  await page.goto('/contact.html')
  await fill(page)
  await page.route('**/api/v1/contact', (route) =>
    route.request().method() === 'POST' ? route.abort('failed') : route.continue(),
  )

  await page.getByRole('button', { name: 'Send message' }).click()

  await expect(page.getByRole('alert')).toContainText('Your message was not sent.', {
    timeout: 60_000,
  })
  await expect(page.getByRole('status')).not.toContainText('Thank you')

  await page.unroute('**/api/v1/contact')
  await page.getByRole('button', { name: 'Send message' }).click()
  await expect(page.getByRole('status')).toContainText('Thank you. Your message was sent.', {
    timeout: 60_000,
  })
})

test('keeps the honeypot out of the tab order and out of display none', async ({ page }) => {
  await page.goto('/contact.html')

  // The harness has no Tailwind, so the off screen class has no CSS here. The
  // class itself is asserted in the unit test.
  const trap = page.locator('input[name="website"]')
  expect(await trap.evaluate((el) => getComputedStyle(el).display)).not.toBe('none')
  await expect(trap).toHaveAttribute('tabindex', '-1')
  await nameField(page).focus()
  for (let i = 0; i < 6; i++) {
    await page.keyboard.press('Tab')
    await expect(trap).not.toBeFocused()
  }
})
