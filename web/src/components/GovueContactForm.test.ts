// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import userEvent from '@testing-library/user-event'
import { render, screen } from '@testing-library/vue'
import { createHash } from 'node:crypto'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { createClient } from '../api/client'
import { createGovueI18n } from '../i18n/plugin'
import { solve } from '../lib/powSolve'
import GovueContactForm from './GovueContactForm.vue'

const realSetTimeout = globalThis.setTimeout

/** A Worker that runs the real search, so the real worker protocol is covered. */
class FakeWorker {
  static created: Array<{ url: string; options: unknown }> = []
  onmessage: ((event: { data: { number: number | null } }) => void) | null = null
  onerror: (() => void) | null = null
  constructor(url: URL | string, options?: unknown) {
    FakeWorker.created.push({ url: String(url), options })
  }
  postMessage(data: { challenge: string; salt: string; maxnumber: number }) {
    void solve(data.challenge, data.salt, data.maxnumber).then((number) =>
      this.onmessage?.({ data: { number } }),
    )
  }
  terminate() {}
}

function challengeBody(issued: number) {
  const salt = `00112233445566778899aabb?expires=${issued + 600}&issued=${issued}&`
  return {
    algorithm: 'SHA-256',
    challenge: createHash('sha256').update(`${salt}7`).digest('hex'),
    maxnumber: 50,
    salt,
    signature: 'f'.repeat(64),
  }
}

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function refusal(status: number, code: string, details?: Record<string, string>): Response {
  return json(status, { error: { code, message: 'An English server text.', details } })
}

interface Seen {
  url: string
  method: string
  headers: Record<string, string>
  body: unknown
  credentials: RequestCredentials | undefined
}

function setup(
  props: Record<string, unknown> = {},
  answer: () => Response | Promise<Response> = () => json(200, { ok: true }),
  slots: Record<string, string> = {},
  locale = 'en',
) {
  const seen: Seen[] = []
  const fetchMock = vi.fn<typeof globalThis.fetch>(async (input, init) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
    seen.push({
      url,
      method: init?.method ?? 'GET',
      headers: (init?.headers ?? {}) as Record<string, string>,
      body: typeof init?.body === 'string' ? JSON.parse(init.body) : undefined,
      credentials: init?.credentials,
    })
    if (url.endsWith('challenge')) {
      return json(200, challengeBody(Math.floor(Date.now() / 1000)))
    }
    return answer()
  })
  const client = createClient({ fetch: fetchMock, credentials: 'omit' })
  const view = render(GovueContactForm, {
    props: { client, ...props },
    slots,
    global: {
      plugins: [
        createGovueI18n({
          locale,
          storage: { kind: 'custom', get: () => undefined, set: () => undefined },
        }),
      ],
    },
  })
  const user = userEvent.setup({ advanceTimers: (ms) => vi.advanceTimersByTime(ms) })
  const submits = () => seen.filter((s) => s.method === 'POST')
  return { fetchMock, seen, submits, user, ...view }
}

type User = ReturnType<typeof userEvent.setup>

async function fill(user: User) {
  await user.type(screen.getByLabelText(/^Name/), 'Alice')
  await user.type(screen.getByLabelText(/^Email address/), 'alice@example.com')
  await user.type(screen.getByLabelText(/^Message/), 'Hello there')
}

/** Lets the real search and the faked minimum age wait run to the end. */
async function settle(until: () => boolean) {
  for (let i = 0; i < 80 && !until(); i++) {
    await vi.advanceTimersByTimeAsync(250)
    await new Promise((resolve) => realSetTimeout(resolve, 5))
  }
}

let cookieReads = 0
let cookieWrites = 0

beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: false })
  FakeWorker.created = []
  vi.stubGlobal('Worker', FakeWorker)
  cookieReads = 0
  cookieWrites = 0
  Object.defineProperty(document, 'cookie', {
    configurable: true,
    get: () => {
      cookieReads++
      return ''
    },
    set: () => {
      cookieWrites++
    },
  })
})

afterEach(() => {
  delete (document as unknown as Record<string, unknown>).cookie
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

describe('GovueContactForm structure', () => {
  it('labels every field and names the form after its heading', () => {
    setup()

    expect(screen.getByRole('form', { name: 'Contact' })).toBeVisible()
    for (const label of [/^Name/, /^Email address/, /^Message/]) {
      expect(screen.getByLabelText(label)).toBeRequired()
    }
    expect(screen.getByLabelText(/^Email address/)).toHaveAttribute('type', 'email')
    expect(screen.getByLabelText(/^Email address/)).toHaveAttribute('autocomplete', 'email')
    expect(screen.getByRole('button', { name: 'Send message' })).toHaveAttribute('type', 'submit')
  })

  it('has an always present polite status region', () => {
    setup()
    const status = screen.getByRole('status')
    expect(status).toHaveAttribute('aria-live', 'polite')
    expect(status).toHaveTextContent('')
  })

  it('puts the honeypot off screen, never behind display none or hidden', () => {
    const { container } = setup()

    const trap = container.querySelector('input[name="website"]') as HTMLInputElement
    expect(trap).not.toBeNull()
    expect(trap).toHaveAttribute('tabindex', '-1')
    expect(trap).toHaveAttribute('autocomplete', 'off')
    const wrapper = trap.closest('[aria-hidden="true"]') as HTMLElement
    expect(wrapper).not.toBeNull()
    expect(wrapper.className).toContain('absolute')
    expect(wrapper.className).toContain('-left-[9999px]')
    for (const element of [trap, wrapper]) {
      expect(element.style.display).not.toBe('none')
      expect(element.hasAttribute('hidden')).toBe(false)
      expect(element.className.split(/\s+/)).not.toContain('hidden')
      expect(element.getAttribute('type')).not.toBe('hidden')
    }
  })

  it('shows no consent checkbox and no privacy link by default', () => {
    setup()
    expect(screen.queryByRole('checkbox')).toBeNull()
    expect(screen.queryByRole('link')).toBeNull()
  })

  it('links the privacy page with a 44px target', () => {
    setup({ privacyHref: '/en/privacy' })
    const link = screen.getByRole('link', { name: 'Privacy policy' })
    expect(link).toHaveAttribute('href', '/en/privacy')
    expect(link.className).toContain('min-h-11')
  })

  it('takes the title and the consent label from slots', () => {
    setup({ requireConsent: true }, undefined, {
      title: 'Write to us',
      consent: 'I accept the <a href="/p">terms</a>',
    })
    expect(screen.getByRole('heading', { name: 'Write to us' })).toBeVisible()
    expect(screen.getByRole('checkbox', { name: 'I accept the terms' })).toBeVisible()
  })

  it('speaks German in a German page', () => {
    setup({}, undefined, {}, 'de')
    expect(screen.getByRole('button', { name: 'Nachricht senden' })).toBeVisible()
    expect(screen.getByLabelText(/^E-Mail-Adresse/)).toBeVisible()
  })
})

describe('GovueContactForm checks before sending', () => {
  it('asks for every field and sends nothing', async () => {
    const { user, seen } = setup()

    await user.click(screen.getByRole('button', { name: 'Send message' }))

    expect(screen.getAllByText('Please fill in this field.')).toHaveLength(3)
    expect(screen.getByLabelText(/^Name/)).toHaveAttribute('aria-invalid', 'true')
    expect(screen.getByLabelText(/^Name/)).toHaveAccessibleDescription('Please fill in this field.')
    expect(seen.filter((s) => s.method === 'POST')).toHaveLength(0)
  })

  it('counts a message of more than 5000 characters as too long', async () => {
    const { user, submits } = setup()
    await user.type(screen.getByLabelText(/^Name/), 'Alice')
    await user.type(screen.getByLabelText(/^Email address/), 'a@example.com')
    await user.click(screen.getByLabelText(/^Message/))
    await user.paste('x'.repeat(5001))

    await user.click(screen.getByRole('button', { name: 'Send message' }))

    expect(screen.getByText('This is too long.')).toBeVisible()
    expect(submits()).toHaveLength(0)
  })

  it('needs the consent when it is required', async () => {
    const { user, submits } = setup({ requireConsent: true })
    await fill(user)

    await user.click(screen.getByRole('button', { name: 'Send message' }))

    expect(screen.getByRole('alert')).toHaveTextContent('Please agree before sending.')
    expect(screen.getByRole('checkbox')).toHaveAttribute('aria-invalid', 'true')
    expect(submits()).toHaveLength(0)
  })
})

describe('GovueContactForm sending', () => {
  it('fetches the challenge on the first focus, once', async () => {
    const { user, seen } = setup()

    expect(seen).toHaveLength(0)
    await user.click(screen.getByLabelText(/^Name/))
    await user.click(screen.getByLabelText(/^Email address/))

    const challenges = seen.filter((s) => s.url.endsWith('/contact/challenge'))
    expect(challenges).toHaveLength(1)
    expect(challenges[0]?.method).toBe('GET')
  })

  it('sends strict JSON with the solution and shows success only after the answer', async () => {
    const { user, seen, submits, emitted } = setup()
    await fill(user)

    await user.click(screen.getByRole('button', { name: 'Send message' }))
    await settle(() => submits().length > 0)
    await vi.advanceTimersByTimeAsync(0)
    await settle(() => screen.queryByText('Thank you. Your message was sent.') !== null)

    expect(submits()).toHaveLength(1)
    const post = submits()[0]
    expect(post.url).toBe('/api/v1/contact')
    expect(post.headers['Content-Type']).toBe('application/json')
    expect(Object.keys(post.body as object).sort()).toEqual([
      'consent',
      'email',
      'message',
      'name',
      'pow',
      'website',
    ])
    expect(post.body).toMatchObject({
      name: 'Alice',
      email: 'alice@example.com',
      message: 'Hello there',
      website: '',
      pow: { algorithm: 'SHA-256', number: 7 },
    })
    expect(screen.getByRole('status')).toHaveTextContent('Thank you. Your message was sent.')
    expect(emitted().sent).toHaveLength(1)
    expect(seen.length).toBeGreaterThan(1)
  })

  it('waits out the minimum age before it sends', async () => {
    const { user, submits } = setup()
    await fill(user)
    const started = Date.now()

    await user.click(screen.getByRole('button', { name: 'Send message' }))
    await settle(() => submits().length > 0)

    expect(Date.now() - started).toBeGreaterThanOrEqual(2000)
    expect(submits()).toHaveLength(1)
  })

  it('never shows success for a failed send', async () => {
    const { user, submits, emitted } = setup({}, () => refusal(503, 'unavailable'))
    await fill(user)

    await user.click(screen.getByRole('button', { name: 'Send message' }))
    await settle(() => screen.queryByRole('alert') !== null)

    expect(submits()).toHaveLength(1)
    expect(screen.getByRole('alert')).toHaveTextContent(
      'Your message was not sent. The contact form is not available right now.',
    )
    expect(screen.queryByText('Thank you. Your message was sent.')).toBeNull()
    expect(screen.getByRole('status')).not.toHaveTextContent('Thank you')
    expect(emitted().sent).toBeUndefined()
    // What the visitor typed is still there, and the form can be sent again.
    expect(screen.getByLabelText(/^Message/)).toHaveValue('Hello there')
    expect(screen.getByRole('button', { name: 'Send message' })).toBeEnabled()
  })

  it('never shows success when the network is down', async () => {
    const { user } = setup({}, () => {
      throw new TypeError('Failed to fetch')
    })
    await fill(user)

    await user.click(screen.getByRole('button', { name: 'Send message' }))
    await settle(() => screen.queryByRole('alert') !== null)

    expect(screen.getByRole('alert')).toHaveTextContent('No connection to the server.')
    expect(screen.queryByText('Thank you. Your message was sent.')).toBeNull()
  })

  it('says rate limited in words, never the server text', async () => {
    const { user } = setup({}, () => refusal(429, 'too_many_requests'))
    await fill(user)

    await user.click(screen.getByRole('button', { name: 'Send message' }))
    await settle(() => screen.queryByRole('alert') !== null)

    expect(screen.getByRole('alert')).toHaveTextContent('Too many messages.')
    expect(screen.getByRole('alert')).not.toHaveTextContent('English server text')
  })

  it('puts a field code on its field, associated and announced', async () => {
    const { user } = setup({}, () => refusal(400, 'invalid_argument', { email: 'non_ascii_email' }))
    await fill(user)

    await user.click(screen.getByRole('button', { name: 'Send message' }))
    await settle(() => screen.queryByText(/plain letters/) !== null)

    const email = screen.getByLabelText(/^Email address/)
    expect(email).toHaveAttribute('aria-invalid', 'true')
    expect(email).toHaveAccessibleDescription(/plain letters, digits and signs only/)
    expect(screen.getByText(/plain letters/)).toHaveAttribute('role', 'alert')
    expect(screen.queryByText('Thank you. Your message was sent.')).toBeNull()
  })

  it('asks for a new challenge after a failed send', async () => {
    const { user, seen } = setup({}, () => refusal(503, 'unavailable'))
    await fill(user)

    await user.click(screen.getByRole('button', { name: 'Send message' }))
    await settle(() => screen.queryByRole('alert') !== null)
    await vi.advanceTimersByTimeAsync(0)

    expect(seen.filter((s) => s.url.endsWith('/contact/challenge'))).toHaveLength(2)
  })

  it('answers a filled honeypot with success and sends nothing', async () => {
    const { user, seen, container } = setup()
    await fill(user)
    const trap = container.querySelector('input[name="website"]') as HTMLInputElement
    await user.type(trap, 'http://spam.example', { skipClick: false })

    await user.click(screen.getByRole('button', { name: 'Send message' }))
    await vi.advanceTimersByTimeAsync(10)

    expect(screen.getByRole('status')).toHaveTextContent('Thank you. Your message was sent.')
    expect(seen.filter((s) => s.method === 'POST')).toHaveLength(0)
  })

  it('offers another message after success', async () => {
    const { user, submits } = setup()
    await fill(user)
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    await settle(() => screen.queryByRole('button', { name: 'Write another message' }) !== null)
    expect(submits()).toHaveLength(1)

    await user.click(screen.getByRole('button', { name: 'Write another message' }))

    expect(screen.getByLabelText(/^Message/)).toHaveValue('')
    expect(screen.getByRole('status')).toHaveTextContent('')
  })
})

describe('GovueContactForm privacy boundaries', () => {
  async function sendOnce(props: Record<string, unknown> = {}) {
    const ctx = setup(props)
    await fill(ctx.user)
    await ctx.user.click(screen.getByRole('button', { name: 'Send message' }))
    await settle(() => ctx.submits().length > 0)
    return ctx
  }

  it('calls only the same origin', async () => {
    const { seen } = await sendOnce()

    expect(seen.length).toBeGreaterThanOrEqual(2)
    for (const request of seen) {
      expect(new URL(request.url, window.location.origin).origin, request.url).toBe(
        window.location.origin,
      )
    }
  })

  it('omits credentials on every request', async () => {
    const { seen } = await sendOnce()
    for (const request of seen) {
      expect(request.credentials, request.url).toBe('omit')
    }
  })

  it('sends no CSRF header and no cookie header', async () => {
    const { seen } = await sendOnce()
    for (const request of seen) {
      const names = Object.keys(request.headers).map((name) => name.toLowerCase())
      expect(names, request.url).not.toContain('x-csrf-token')
      expect(names, request.url).not.toContain('cookie')
      expect(names, request.url).not.toContain('authorization')
    }
  })

  it('reads and writes no cookie', async () => {
    await sendOnce()
    expect(cookieReads).toBe(0)
    expect(cookieWrites).toBe(0)
  })

  it('starts its worker as a module worker from the package, and only after a focus', async () => {
    const { user } = setup()
    expect(FakeWorker.created).toHaveLength(0)

    await user.click(screen.getByLabelText(/^Name/))
    await settle(() => FakeWorker.created.length > 0)

    expect(FakeWorker.created).toHaveLength(1)
    expect(FakeWorker.created[0]?.url).toMatch(/\/src\/lib\/pow\.worker\.ts/)
    expect(FakeWorker.created[0]?.options).toEqual({ type: 'module' })
  })

  it('uses the routes it is given', async () => {
    const { user, seen } = setup({ challengePath: '/x/challenge', submitPath: '/x/send' })
    await fill(user)

    await user.click(screen.getByRole('button', { name: 'Send message' }))
    await settle(() => seen.some((s) => s.method === 'POST'))

    expect(seen.map((s) => s.url)).toEqual(['/api/v1/x/challenge', '/api/v1/x/send'])
  })
})
