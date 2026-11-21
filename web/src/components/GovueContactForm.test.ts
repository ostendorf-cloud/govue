// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import userEvent from '@testing-library/user-event'
import { render, screen } from '@testing-library/vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'

import { createClient } from '../api/client'
import { FakeContactServer, refusal } from '../../example/contactServerFake'
import { createGovueI18n } from '../i18n/plugin'
import { solve } from '../lib/powSolve'
import GovueContactForm from './GovueContactForm.vue'

const realSetTimeout = globalThis.setTimeout

type WorkerMode = 'solve' | 'error' | 'silent'

/** A Worker that runs the real search, or fails, or never answers. */
class FakeWorker {
  static mode: WorkerMode = 'solve'
  static created: Array<{ url: string; options: unknown }> = []
  static terminated = 0
  onmessage: ((event: { data: { number: number | null } }) => void) | null = null
  onerror: (() => void) | null = null
  onmessageerror: (() => void) | null = null
  constructor(url: URL | string, options?: unknown) {
    FakeWorker.created.push({ url: String(url), options })
  }
  postMessage(data: { challenge: string; salt: string; maxnumber: number }) {
    if (FakeWorker.mode === 'solve') {
      void solve(data.challenge, data.salt, data.maxnumber).then((number) =>
        this.onmessage?.({ data: { number } }),
      )
    } else if (FakeWorker.mode === 'error') {
      queueMicrotask(() => this.onerror?.())
    }
  }
  terminate() {
    FakeWorker.terminated++
  }
}

function setup(
  props: Record<string, unknown> = {},
  options: { slots?: Record<string, string>; locale?: string; server?: FakeContactServer } = {},
) {
  const server = options.server ?? new FakeContactServer()
  // The global fetch, which createClient binds when it is created: the form
  // goes through the client's own defaults and nothing else.
  const fetchSpy = vi.spyOn(globalThis, 'fetch').mockImplementation(server.fetch)
  const client = createClient()
  const view = render(GovueContactForm, {
    props: { client, ...props },
    slots: options.slots ?? {},
    global: {
      plugins: [
        createGovueI18n({
          locale: options.locale ?? 'en',
          storage: { kind: 'custom', get: () => undefined, set: () => undefined },
        }),
      ],
    },
  })
  const user = userEvent.setup({ advanceTimers: (ms) => vi.advanceTimersByTime(ms) })
  return { server, fetchSpy, user, ...view }
}

type User = ReturnType<typeof userEvent.setup>

async function fill(user: User) {
  await user.type(screen.getByLabelText(/^Name/), 'Alice')
  await user.type(screen.getByLabelText(/^Email address/), 'alice@example.com')
  await user.type(screen.getByLabelText(/^Message/), 'Hello there')
}

/** Lets the real search and the faked waits run until the condition holds. */
async function settle(until: () => boolean, steps = 80) {
  for (let i = 0; i < steps && !until(); i++) {
    await vi.advanceTimersByTimeAsync(250)
    await new Promise((resolve) => realSetTimeout(resolve, 5))
  }
}

const CONSENT = 'I agree to the test terms.'

const send = () => screen.getByRole('button', { name: 'Send message' })
const thanks = () => screen.queryByText('Thank you. Your message was sent.')

let cookieReads = 0
let cookieWrites = 0

beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: false })
  FakeWorker.mode = 'solve'
  FakeWorker.created = []
  FakeWorker.terminated = 0
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
  vi.restoreAllMocks()
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
    expect(send()).toHaveAttribute('type', 'submit')
  })

  it('has an always present polite status region', () => {
    setup()
    const status = screen.getByRole('status')
    expect(status).toHaveAttribute('aria-live', 'polite')
    expect(status).toHaveTextContent('')
  })

  it('renders the title as h2 by default and at the level it is given', () => {
    const first = setup()
    expect(screen.getByRole('heading', { level: 2, name: 'Contact' })).toBeVisible()
    first.unmount()

    setup({ headingLevel: 4 })
    expect(screen.getByRole('heading', { level: 4, name: 'Contact' })).toBeVisible()
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
    setup(
      { requireConsent: true },
      { slots: { title: 'Write to us', consent: 'I accept the <a href="/p">terms</a>' } },
    )
    expect(screen.getByRole('heading', { name: 'Write to us' })).toBeVisible()
    expect(screen.getByRole('checkbox', { name: 'I accept the terms' })).toBeVisible()
  })

  it('takes the consent text from the consentLabel prop', () => {
    setup({ requireConsent: true, consentLabel: CONSENT })
    expect(screen.getByRole('checkbox', { name: CONSENT })).toBeVisible()
  })

  it('has no consent text of its own: without one from the site it renders nothing', () => {
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => undefined)
    const { container } = setup({ requireConsent: true })

    expect(screen.queryByRole('checkbox')).toBeNull()
    expect(screen.queryByRole('form')).toBeNull()
    expect(container.querySelector('input')).toBeNull()
    expect(warnSpy.mock.calls.flat().join(' ')).toContain('requireConsent needs a consent text')
  })

  it('renders no checkbox when consent is not required, even with a text', () => {
    setup({ requireConsent: false, consentLabel: CONSENT })
    expect(screen.queryByRole('checkbox')).toBeNull()
  })

  it('refuses a bearer client: nothing is rendered and nothing is sent', async () => {
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => undefined)
    const fetchMock = vi.fn<typeof globalThis.fetch>()
    const client = createClient({ fetch: fetchMock, auth: { mode: 'bearer' } })
    const { container } = render(GovueContactForm, { props: { client } })
    await nextTick()

    expect(container.querySelector('form')).toBeNull()
    expect(container.querySelector('input')).toBeNull()
    expect(fetchMock).not.toHaveBeenCalled()
    expect(warnSpy.mock.calls.flat().join(' ')).toContain('bearer client')
  })

  it('speaks German in a German page', () => {
    setup({}, { locale: 'de' })
    expect(screen.getByRole('button', { name: 'Nachricht senden' })).toBeVisible()
    expect(screen.getByLabelText(/^E-Mail-Adresse/)).toBeVisible()
  })
})

describe('GovueContactForm checks before sending', () => {
  it('asks for every field, sends nothing and focuses the first error', async () => {
    const { user, server } = setup()

    await user.click(send())

    expect(screen.getAllByText('Please fill in this field.')).toHaveLength(3)
    expect(screen.getByLabelText(/^Name/)).toHaveAttribute('aria-invalid', 'true')
    expect(screen.getByLabelText(/^Name/)).toHaveAccessibleDescription('Please fill in this field.')
    expect(screen.getByLabelText(/^Name/)).toHaveFocus()
    expect(server.submits).toHaveLength(0)
  })

  it('names the limit when a message is too long', async () => {
    const { user, server } = setup()
    await user.type(screen.getByLabelText(/^Name/), 'Alice')
    await user.type(screen.getByLabelText(/^Email address/), 'a@example.com')
    await user.click(screen.getByLabelText(/^Message/))
    await user.paste('x'.repeat(5001))

    await user.click(send())

    expect(screen.getByText('This is too long. At most 5000 characters are allowed.')).toBeVisible()
    expect(screen.getByLabelText(/^Message/)).toHaveFocus()
    expect(server.submits).toHaveLength(0)
  })

  it('needs the consent when it is required, and associates the error with the box', async () => {
    const { user, server } = setup({ requireConsent: true, consentLabel: CONSENT })
    await fill(user)

    await user.click(send())

    const box = screen.getByRole('checkbox')
    expect(screen.getByRole('alert')).toHaveTextContent('Please agree before sending.')
    expect(box).toHaveAttribute('aria-invalid', 'true')
    expect(box).toHaveAccessibleDescription('Please agree before sending.')
    expect(box).toHaveFocus()
    expect(server.submits).toHaveLength(0)
  })
})

describe('GovueContactForm sending', () => {
  it('fetches the challenge on the first focus, once', async () => {
    const { user, server } = setup()

    expect(server.requests).toHaveLength(0)
    await user.click(screen.getByLabelText(/^Name/))
    await user.click(screen.getByLabelText(/^Email address/))

    expect(server.challenges).toHaveLength(1)
  })

  it('sends exactly the fields the server knows, and the server accepts them', async () => {
    const { user, server } = setup({ requireConsent: true, consentLabel: CONSENT })
    await fill(user)
    await user.click(screen.getByRole('checkbox'))

    await user.click(send())
    await settle(() => thanks() !== null)

    expect(server.submits).toHaveLength(1)
    const post = server.submits[0]
    expect(post.url).toBe('/api/v1/contact')
    expect(post.headers['Content-Type']).toBe('application/json')
    // consent stays in the browser: the server refuses an unknown field.
    expect(Object.keys(post.body as object).sort()).toEqual([
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
      pow: { algorithm: 'SHA-256', number: expect.any(Number) as number },
    })
    expect(Object.keys((post.body as { pow: object }).pow).sort()).toEqual([
      'algorithm',
      'challenge',
      'number',
      'salt',
      'signature',
    ])
    expect(screen.getByRole('status')).toHaveTextContent('Thank you. Your message was sent.')
  })

  it('shows success only after the server answered, emits sent and focuses the status', async () => {
    const { user, emitted } = setup()
    await fill(user)

    await user.click(send())
    await settle(() => thanks() !== null)

    expect(emitted().sent).toHaveLength(1)
    // The form and its focused button are gone: focus moves to the status.
    expect(screen.queryByRole('form')).toBeNull()
    expect(screen.getByRole('status')).toHaveFocus()
  })

  it('waits out the minimum age before it sends', async () => {
    const { user, server } = setup()
    await fill(user)
    const started = Date.now()

    await user.click(send())
    await settle(() => server.submits.length > 0)

    expect(Date.now() - started).toBeGreaterThanOrEqual(2000)
    expect(server.submits).toHaveLength(1)
  })

  it('takes the minimum age from its prop: too short is refused as too fast', async () => {
    const { user, server } = setup({ minAgeMs: 0 })
    await fill(user)

    await user.click(send())
    await settle(() => screen.queryByRole('alert') !== null)

    expect(server.submits).toHaveLength(1)
    expect(screen.getByRole('alert')).toHaveTextContent('The form was sent too fast.')
    expect(thanks()).toBeNull()
  })

  it('never shows success for a failed send, keeps the input and focuses the failure', async () => {
    const server = new FakeContactServer()
    server.refuseNext(() => refusal(503, 'send_failed'))
    const { user, emitted } = setup({}, { server })
    await fill(user)

    await user.click(send())
    await settle(() => screen.queryByRole('alert') !== null)

    expect(server.submits).toHaveLength(1)
    expect(screen.getByRole('alert')).toHaveTextContent(
      'Sending failed. Your message may not have been sent. Please check before you send it again or write by another way.',
    )
    expect(thanks()).toBeNull()
    expect(screen.getByRole('status')).not.toHaveTextContent('Thank you')
    expect(emitted().sent).toBeUndefined()
    expect(screen.getByLabelText(/^Message/)).toHaveValue('Hello there')
    expect(send()).toBeEnabled()
    expect(screen.getByRole('alert')).toHaveFocus()
  })

  it('never shows success when the network is down', async () => {
    const { user, fetchSpy, server } = setup()
    fetchSpy.mockImplementation((input, init) => {
      const url = typeof input === 'string' ? input : ''
      if (url.endsWith('/challenge')) return server.fetch(input, init)
      return Promise.reject(new TypeError('Failed to fetch'))
    })
    await fill(user)

    await user.click(send())
    await settle(() => screen.queryByRole('alert') !== null)

    expect(screen.getByRole('alert')).toHaveTextContent('No connection to the server.')
    expect(thanks()).toBeNull()
  })

  // One row per real code of contact/handler.go and the api package, with the
  // status the server answers it with and a word of the text the visitor sees.
  const refusals: Array<[number, string, string]> = [
    [403, 'cross_origin', 'The request was refused.'],
    [503, 'not_configured', 'not set up yet'],
    [400, 'pow_required', 'The spam check is missing.'],
    [400, 'pow_invalid', 'The spam check failed.'],
    [400, 'pow_expired', 'The spam check ran out of time.'],
    [400, 'pow_too_fast', 'sent too fast'],
    [400, 'pow_replay', 'already used'],
    [503, 'pow_busy', 'The server is busy'],
    [503, 'mail_limit', 'reached its limit'],
    [503, 'send_failed', 'may not have been sent'],
    [429, 'too_many_requests', 'Too many messages.'],
    [413, 'request_too_large', 'too large'],
    [415, 'unsupported_media_type', 'could not be accepted'],
    [400, 'invalid_argument', 'could not be accepted'],
  ]
  for (const [status, code, text] of refusals) {
    it(`shows the text of ${code} (${status}) and never the server text`, async () => {
      const server = new FakeContactServer()
      server.refuseNext(() => refusal(status, code))
      const { user } = setup({}, { server })
      await fill(user)

      await user.click(send())
      await settle(() => screen.queryByRole('alert') !== null)

      expect(screen.getByRole('alert')).toHaveTextContent(text)
      expect(screen.getByRole('alert')).not.toHaveTextContent('English server text')
      expect(thanks()).toBeNull()
    })
  }

  describe('with a Retry-After', () => {
    const waits: Array<[string, number, string, string]> = [
      ['too_many_requests', 429, '300', 'Too many messages. Please try again in about 5 minutes.'],
      ['too_many_requests', 429, '30', 'Please try again in about a minute.'],
      ['mail_limit', 503, '1800', 'Nothing was sent. Please try again in about 30 minutes.'],
      ['pow_busy', 503, '7200', 'Nothing was sent. Please try again in about 2 hours.'],
    ]
    for (const [code, status, header, text] of waits) {
      it(`names the wait of ${code} (${header} s)`, async () => {
        const server = new FakeContactServer()
        server.refuseNext(() =>
          refusal(status, code, undefined, undefined, { 'Retry-After': header }),
        )
        const { user } = setup({}, { server })
        await fill(user)

        await user.click(send())
        await settle(() => screen.queryByRole('alert') !== null)

        expect(screen.getByRole('alert')).toHaveTextContent(text)
      })
    }

    it('says it in German too', async () => {
      const server = new FakeContactServer()
      server.refuseNext(() =>
        refusal(429, 'too_many_requests', undefined, undefined, { 'Retry-After': '300' }),
      )
      const { user } = setup({}, { server, locale: 'de' })
      await user.type(screen.getByLabelText(/^Name/), 'Alice')
      await user.type(screen.getByLabelText(/^E-Mail-Adresse/), 'alice@example.com')
      await user.type(screen.getByLabelText(/^Nachricht/), 'Hallo')

      await user.click(screen.getByRole('button', { name: 'Nachricht senden' }))
      await settle(() => screen.queryByRole('alert') !== null)

      expect(screen.getByRole('alert')).toHaveTextContent(
        'Zu viele Nachrichten. Bitte versuche es in etwa 5 Minuten erneut.',
      )
    })

    it('keeps the plain text when the header is not a number of seconds', async () => {
      const server = new FakeContactServer()
      server.refuseNext(() =>
        refusal(429, 'too_many_requests', undefined, undefined, { 'Retry-After': 'tomorrow' }),
      )
      const { user } = setup({}, { server })
      await fill(user)

      await user.click(send())
      await settle(() => screen.queryByRole('alert') !== null)

      expect(screen.getByRole('alert')).toHaveTextContent(
        'Too many messages. Please wait a few minutes and try again.',
      )
    })
  })

  it('puts a field code on its field, associated, announced and focused', async () => {
    const server = new FakeContactServer()
    server.refuseNext(() =>
      refusal(400, 'invalid_argument', 'some fields are invalid', { email: 'non_ascii_email' }),
    )
    const { user } = setup({}, { server })
    await fill(user)

    await user.click(send())
    await settle(() => screen.queryByText(/umlauts/) !== null)

    const email = screen.getByLabelText(/^Email address/)
    expect(email).toHaveAttribute('aria-invalid', 'true')
    expect(email).toHaveAccessibleDescription(/without umlauts or other special characters/)
    expect(screen.getByText(/umlauts/)).toHaveAttribute('role', 'alert')
    expect(email).toHaveFocus()
    expect(thanks()).toBeNull()
  })

  it('shows the real field answers of the server on their fields', async () => {
    const { user, server } = setup()
    await user.type(screen.getByLabelText(/^Name/), 'Alice')
    await user.type(screen.getByLabelText(/^Email address/), 'not-an-address')
    await user.type(screen.getByLabelText(/^Message/), 'Hello')

    await user.click(send())
    await settle(() => screen.queryByText('This is not valid.') !== null)

    expect(server.submits).toHaveLength(1)
    expect(screen.getByLabelText(/^Email address/)).toHaveAccessibleDescription(
      'This is not valid.',
    )
  })

  it('asks for a new challenge after a failed send', async () => {
    const server = new FakeContactServer()
    server.refuseNext(() => refusal(503, 'send_failed'))
    const { user } = setup({}, { server })
    await fill(user)

    await user.click(send())
    await settle(() => screen.queryByRole('alert') !== null)
    await vi.advanceTimersByTimeAsync(0)

    expect(server.challenges).toHaveLength(2)
  })

  it('answers a filled honeypot with success and sends nothing', async () => {
    const { user, server, container } = setup()
    await fill(user)
    const trap = container.querySelector('input[name="website"]') as HTMLInputElement
    await user.type(trap, 'http://spam.example')

    await user.click(send())
    await vi.advanceTimersByTimeAsync(10)

    expect(screen.getByRole('status')).toHaveTextContent('Thank you. Your message was sent.')
    expect(server.submits).toHaveLength(0)
  })

  it('offers another message after success and returns focus to the first field', async () => {
    const { user, server } = setup()
    await fill(user)
    await user.click(send())
    await settle(() => screen.queryByRole('button', { name: 'Write another message' }) !== null)
    expect(server.submits).toHaveLength(1)

    await user.click(screen.getByRole('button', { name: 'Write another message' }))

    expect(screen.getByLabelText(/^Message/)).toHaveValue('')
    expect(screen.getByRole('status')).toHaveTextContent('')
    expect(screen.getByLabelText(/^Name/)).toHaveFocus()
  })
})

describe('GovueContactForm solver failures', () => {
  it('says the spam check could not run when the worker fails, not that the network is down', async () => {
    FakeWorker.mode = 'error'
    const { user, server } = setup()
    await fill(user)

    await user.click(send())
    await settle(() => screen.queryByRole('alert') !== null)

    expect(screen.getByRole('alert')).toHaveTextContent('The spam check could not run')
    expect(screen.getByRole('alert')).not.toHaveTextContent('No connection')
    expect(server.submits).toHaveLength(0)
    expect(thanks()).toBeNull()
    expect(FakeWorker.terminated).toBeGreaterThan(0)
  })

  it('gives up on a worker that never answers and leaves the sending state', async () => {
    FakeWorker.mode = 'silent'
    const { user, server } = setup()
    await fill(user)

    await user.click(send())
    await settle(() => screen.queryByRole('alert') !== null, 800)

    expect(screen.getByRole('alert')).toHaveTextContent('The spam check could not run')
    expect(send()).toBeEnabled()
    expect(screen.getByRole('status')).toHaveTextContent('')
    expect(server.submits).toHaveLength(0)
    expect(FakeWorker.terminated).toBeGreaterThan(0)
  })

  it('cancels a send that is on its way when the form is removed, and emits no sent', async () => {
    const { user, unmount, emitted, fetchSpy, server } = setup()
    let submitSignal: AbortSignal | null | undefined
    let answer: (response: Response) => void = () => undefined
    fetchSpy.mockImplementation((input, init) => {
      const url = typeof input === 'string' ? input : ''
      if (url.endsWith('/challenge')) return server.fetch(input, init)
      submitSignal = init?.signal
      return new Promise<Response>((resolve) => {
        answer = resolve
      })
    })
    await fill(user)
    await user.click(send())
    await settle(() => submitSignal !== undefined)
    expect(submitSignal?.aborted).toBe(false)

    unmount()
    answer(new Response('{"ok":true}', { status: 200 }))
    await vi.advanceTimersByTimeAsync(0)

    expect(submitSignal?.aborted).toBe(true)
    expect(emitted().sent).toBeUndefined()
  })

  it('terminates a running worker when the form is removed', async () => {
    FakeWorker.mode = 'silent'
    const { user, unmount } = setup()
    await user.click(screen.getByLabelText(/^Name/))
    await settle(() => FakeWorker.created.length > 0)
    expect(FakeWorker.created).toHaveLength(1)
    expect(FakeWorker.terminated).toBe(0)

    unmount()
    await vi.advanceTimersByTimeAsync(0)

    expect(FakeWorker.terminated).toBe(1)
  })
})

describe('GovueContactForm privacy boundaries', () => {
  async function sendOnce(props: Record<string, unknown> = {}) {
    const ctx = setup(props)
    await fill(ctx.user)
    await ctx.user.click(send())
    await settle(() => ctx.server.submits.length > 0)
    return ctx
  }

  it('calls only the same origin, through the global fetch of the default client', async () => {
    const { server, fetchSpy } = await sendOnce()

    expect(fetchSpy.mock.calls.length).toBeGreaterThanOrEqual(2)
    for (const [input] of fetchSpy.mock.calls) {
      const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
      expect(new URL(url, window.location.origin).origin, url).toBe(window.location.origin)
    }
    for (const request of server.requests) {
      expect(new URL(request.url, window.location.origin).origin, request.url).toBe(
        window.location.origin,
      )
    }
  })

  it('adds no header of its own: no CSRF token, no cookie, no authorization', async () => {
    const { server } = await sendOnce()
    expect(server.requests.length).toBeGreaterThanOrEqual(2)
    for (const request of server.requests) {
      const names = Object.keys(request.headers).map((name) => name.toLowerCase())
      expect(
        names.filter((n) => n !== 'accept' && n !== 'content-type'),
        request.url,
      ).toEqual([])
    }
  })

  it('leaves credentials to the client: a default client is same-origin', async () => {
    // The form never sets credentials. A site that wants none creates the client
    // with credentials: 'omit', which is the caller's duty, documented on the prop.
    const { server } = await sendOnce()
    for (const request of server.requests) {
      expect(request.credentials, request.url).toBe('same-origin')
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
    const { user, server } = setup({ challengePath: '/x/challenge', submitPath: '/x/send' })
    await fill(user)

    await user.click(send())
    await settle(() => server.submits.length > 0)

    expect(server.requests.map((s) => s.url)).toEqual(['/api/v1/x/challenge', '/api/v1/x/send'])
  })
})
