// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// The contact form in a real Chromium: the real component, the real worker
// file and the real client.
//
// The server half (govue #197) is not part of this suite. page.route answers
// the two routes with a stub that follows the contract of the Go pow package:
// a signed challenge, the hash of salt + number with the trailing separator,
// the minimum age, the expiry and single use. The stub is no substitute for the
// Go handlers: the e2e against the example server follows when #197 is merged.
import { createHmac, createHash, randomBytes } from 'node:crypto'

import { expect, test, type Page, type Route } from '@playwright/test'

const ORIGIN = 'http://localhost:4173'
const KEY = Buffer.from('0123456789abcdef0123456789abcdef')
const AUDIENCE = 'e2e.test'
const MAX_NUMBER = 20_000
const MIN_AGE_SECONDS = 3

interface Stub {
  /** Every request the stub answered, as "METHOD path". */
  seen: string[]
  /** The bodies of the submit requests. */
  bodies: Record<string, unknown>[]
  /** Answer the next submit with this refusal instead of checking it. */
  refuse?: { status: number; code: string; details?: Record<string, string> }
  /** Every cross origin request the page tried. */
  foreign: string[]
}

function sign(challenge: string): string {
  return createHmac('sha256', KEY)
    .update(`govue/pow\x00v1\x00${AUDIENCE}\x00${challenge}`)
    .digest('hex')
}

function reply(route: Route, status: number, body: unknown) {
  return route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
}

function refusal(route: Route, status: number, code: string, details?: Record<string, string>) {
  return reply(route, status, { error: { code, message: 'English text of the server', details } })
}

async function install(page: Page): Promise<Stub> {
  const stub: Stub = { seen: [], bodies: [], foreign: [] }
  const used = new Set<string>()

  page.on('request', (request) => {
    const url = new URL(request.url())
    if (url.origin !== ORIGIN && url.protocol.startsWith('http')) {
      stub.foreign.push(request.url())
    }
  })

  await page.route('**/api/v1/contact/challenge', async (route) => {
    stub.seen.push('GET /contact/challenge')
    const issued = Math.floor(Date.now() / 1000)
    const salt = `${randomBytes(12).toString('hex')}?expires=${issued + 600}&issued=${issued}&`
    const number = Math.floor(Math.random() * (MAX_NUMBER + 1))
    const challenge = createHash('sha256').update(`${salt}${number}`).digest('hex')
    await reply(route, 200, {
      algorithm: 'SHA-256',
      challenge,
      maxnumber: MAX_NUMBER,
      salt,
      signature: sign(challenge),
    })
  })

  await page.route('**/api/v1/contact', async (route) => {
    const request = route.request()
    stub.seen.push(`${request.method()} /contact`)
    const headers = await request.allHeaders()
    if (request.method() !== 'POST' || headers['content-type'] !== 'application/json') {
      return refusal(route, 415, 'unsupported_media_type')
    }
    if (headers['x-csrf-token'] !== undefined || headers['cookie'] !== undefined) {
      throw new Error('contract violation: a CSRF header or a cookie was sent')
    }
    const body = request.postDataJSON() as Record<string, unknown>
    stub.bodies.push(body)
    if (stub.refuse) {
      const { status, code, details } = stub.refuse
      stub.refuse = undefined
      return refusal(route, status, code, details)
    }

    const pow = body.pow as {
      algorithm: string
      challenge: string
      number: number
      salt: string
      signature: string
    }
    const salt = /^[0-9a-f]{24}\?expires=(\d+)&issued=(\d+)&$/.exec(pow.salt)
    const now = Math.floor(Date.now() / 1000)
    const hash = createHash('sha256').update(`${pow.salt}${pow.number}`).digest('hex')
    if (
      !salt ||
      pow.algorithm !== 'SHA-256' ||
      pow.signature !== sign(pow.challenge) ||
      hash !== pow.challenge ||
      now >= Number(salt[1]) ||
      now - Number(salt[2]) < MIN_AGE_SECONDS ||
      used.has(pow.challenge)
    ) {
      return refusal(route, 400, 'invalid_argument', { pow: 'wrong' })
    }
    used.add(pow.challenge)
    return reply(route, 200, {})
  })
  return stub
}

async function fill(page: Page) {
  await page.getByLabel('Name', { exact: false }).first().fill('Alice')
  await page.getByLabel('Email address').fill('alice@example.com')
  await page.getByLabel('Message').fill('Hello from the browser')
  await page.getByRole('checkbox').check()
}

test('solves the challenge in a worker, sends it and shows success', async ({ page }) => {
  const stub = await install(page)
  await page.goto('/contact.html')
  await fill(page)

  await page.getByRole('button', { name: 'Send message' }).click()

  await expect(page.getByRole('status')).toContainText('Thank you. Your message was sent.', {
    timeout: 30_000,
  })
  expect(stub.seen).toEqual(['GET /contact/challenge', 'POST /contact'])
  expect(stub.bodies[0]).toMatchObject({
    name: 'Alice',
    email: 'alice@example.com',
    website: '',
    pow: { algorithm: 'SHA-256' },
  })
  expect(stub.foreign).toEqual([])
  expect(await page.evaluate(() => document.cookie)).toBe('')
})

test('loads the worker file from the same origin as a module worker', async ({ page }) => {
  await install(page)
  const workers: string[] = []
  page.on('worker', (worker) => workers.push(worker.url()))
  await page.goto('/contact.html')

  await page.getByLabel('Name').first().focus()

  await expect.poll(() => workers.length, { timeout: 15_000 }).toBeGreaterThan(0)
  expect(new URL(workers[0]).origin).toBe(ORIGIN)
  expect(workers[0]).toContain('pow.worker')
})

test('a refused send is a failure, never a success, and the next send works', async ({ page }) => {
  const stub = await install(page)
  stub.refuse = { status: 503, code: 'unavailable' }
  await page.goto('/contact.html')
  await fill(page)

  await page.getByRole('button', { name: 'Send message' }).click()

  await expect(page.getByRole('alert')).toContainText('Your message was not sent.', {
    timeout: 30_000,
  })
  await expect(page.getByRole('status')).not.toContainText('Thank you')

  await page.getByRole('button', { name: 'Send message' }).click()
  await expect(page.getByRole('status')).toContainText('Thank you. Your message was sent.', {
    timeout: 30_000,
  })
  expect(stub.seen.filter((s) => s === 'GET /contact/challenge').length).toBeGreaterThanOrEqual(2)
})

test('puts a field code from the server on its field', async ({ page }) => {
  const stub = await install(page)
  stub.refuse = { status: 400, code: 'invalid_argument', details: { email: 'invalid' } }
  await page.goto('/contact.html')
  await fill(page)

  await page.getByRole('button', { name: 'Send message' }).click()

  await expect(page.getByLabel('Email address')).toHaveAttribute('aria-invalid', 'true', {
    timeout: 30_000,
  })
  await expect(page.getByLabel('Email address')).toHaveAccessibleDescription('This is not valid.')
})

test('keeps the honeypot out of the tab order and out of display none', async ({ page }) => {
  await install(page)
  await page.goto('/contact.html')

  // The harness has no Tailwind, so the off screen class has no CSS here. The
  // class itself is asserted in the unit test.
  const trap = page.locator('input[name="website"]')
  expect(await trap.evaluate((el) => getComputedStyle(el).display)).not.toBe('none')
  await expect(trap).toHaveAttribute('tabindex', '-1')
  await page.getByLabel('Name').first().focus()
  for (let i = 0; i < 6; i++) {
    await page.keyboard.press('Tab')
    await expect(trap).not.toBeFocused()
  }
})
