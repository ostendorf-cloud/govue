// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import userEvent from '@testing-library/user-event'
import { render, screen } from '@testing-library/vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'

import { createClient } from '../api/client'
import { refusal } from '../../example/contactServerFake'
import { FakeWaitlistServer } from '../../example/waitlistServerFake'
import { createGovueI18n } from '../i18n/plugin'
import { solve } from '../lib/powSolve'
import GovueWaitlistForm from './GovueWaitlistForm.vue'

const realSetTimeout = globalThis.setTimeout

type WorkerMode = 'solve' | 'error' | 'silent'

/** A Worker that runs the real search, or fails, or never answers. */
class FakeWorker {
  static mode: WorkerMode = 'solve'
  static created = 0
  static terminated = 0
  onmessage: ((event: { data: { number: number | null } }) => void) | null = null
  onerror: (() => void) | null = null
  onmessageerror: (() => void) | null = null
  constructor() {
    FakeWorker.created++
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

const CONSENT = 'I agree to receive the launch mail.'

interface SetupOptions {
  slots?: Record<string, string>
  locale?: string
  formality?: 'formal' | 'informal'
  server?: FakeWaitlistServer
}

function setup(
  props: Record<string, unknown> = { consentLabel: CONSENT },
  options: SetupOptions = {},
) {
  const server = options.server ?? new FakeWaitlistServer()
  const fetchSpy = vi.spyOn(globalThis, 'fetch').mockImplementation(server.fetch)
  const client = createClient()
  const view = render(GovueWaitlistForm, {
    props: { client, ...props },
    slots: options.slots ?? {},
    global: {
      plugins: [
        createGovueI18n({
          locale: options.locale ?? 'en',
          formality: options.formality,
          storage: { kind: 'custom', get: () => undefined, set: () => undefined },
        }),
      ],
    },
  })
  const user = userEvent.setup({ advanceTimers: (ms) => vi.advanceTimersByTime(ms) })
  return { server, fetchSpy, user, ...view }
}

type User = ReturnType<typeof userEvent.setup>

async function fill(user: User, address = 'ada@example.com') {
  await user.type(screen.getByLabelText(/^Email address/), address)
  await user.click(screen.getByRole('checkbox'))
}

/** Lets the real search and the faked waits run until the condition holds. */
async function settle(until: () => boolean, steps = 80) {
  for (let i = 0; i < steps && !until(); i++) {
    await vi.advanceTimersByTimeAsync(250)
    await new Promise((resolve) => realSetTimeout(resolve, 5))
  }
}

const join = () => screen.getByRole('button', { name: 'Join the waitlist' })
const inbox = () => screen.queryByText(/Please check your inbox/)

let cookieReads = 0
let cookieWrites = 0

beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: false })
  FakeWorker.mode = 'solve'
  FakeWorker.created = 0
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

describe('GovueWaitlistForm structure', () => {
  it('labels the address field and the consent box, and names the form after its heading', async () => {
    setup()
    await nextTick()

    expect(screen.getByRole('form', { name: 'Waitlist' })).toBeVisible()
    const email = screen.getByLabelText(/^Email address/)
    expect(email).toBeRequired()
    expect(email).toHaveAttribute('type', 'email')
    expect(email).toHaveAttribute('autocomplete', 'email')
    expect(screen.getByRole('checkbox', { name: CONSENT })).not.toBeChecked()
    expect(join()).toHaveAttribute('type', 'submit')
    expect(join()).toBeEnabled()
  })

  it('has an always present polite status region', () => {
    setup()
    const status = screen.getByRole('status')
    expect(status).toHaveAttribute('aria-live', 'polite')
    expect(status).toHaveTextContent('')
  })

  it('renders the title at the level it is given', () => {
    setup({ consentLabel: CONSENT, headingLevel: 3 })
    expect(screen.getByRole('heading', { level: 3, name: 'Waitlist' })).toBeVisible()
  })

  it('says in a noscript block that the form needs JavaScript', () => {
    const { container } = setup()
    const noscript = container.querySelector('noscript')
    expect(noscript).not.toBeNull()
    expect(noscript?.textContent).toContain('This form needs JavaScript')
  })

  it('puts the honeypot off screen, never behind display none or hidden', () => {
    const { container } = setup()
    const trap = container.querySelector('input[name="website"]') as HTMLInputElement
    expect(trap).toHaveAttribute('tabindex', '-1')
    const wrapper = trap.closest('[aria-hidden="true"]') as HTMLElement
    expect(wrapper.className).toContain('-left-[9999px]')
    expect(trap.getAttribute('type')).toBe('text')
  })

  it('links the privacy page with a 44px target', () => {
    setup({ consentLabel: CONSENT, privacyHref: '/en/privacy' })
    const link = screen.getByRole('link', { name: 'Privacy policy' })
    expect(link).toHaveAttribute('href', '/en/privacy')
    expect(link.className).toContain('min-h-11')
  })

  it('takes the title and the consent label from slots', () => {
    setup({}, { slots: { title: 'Get notified', consent: 'I accept the <a href="/p">terms</a>' } })
    expect(screen.getByRole('heading', { name: 'Get notified' })).toBeVisible()
    expect(screen.getByRole('checkbox', { name: 'I accept the terms' })).toBeVisible()
  })

  it('has no consent text of its own: without one from the site it renders nothing', () => {
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => undefined)
    const { container } = setup({})

    expect(screen.queryByRole('form')).toBeNull()
    expect(container.querySelector('input')).toBeNull()
    expect(screen.getByRole('alert')).toHaveTextContent('The waitlist form is not available.')
    expect(warnSpy.mock.calls.flat().join(' ')).toContain('needs a consent text')
  })

  it('shows the form when the consent text arrives late', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => undefined)
    const { rerender } = setup({})
    await rerender({ consentLabel: CONSENT })
    expect(screen.getByRole('form', { name: 'Waitlist' })).toBeVisible()
  })

  it('refuses a bearer client: nothing is rendered and nothing is sent', async () => {
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => undefined)
    const fetchMock = vi.fn<typeof globalThis.fetch>()
    const client = createClient({ fetch: fetchMock, auth: { mode: 'bearer' } })
    const { container } = render(GovueWaitlistForm, { props: { client, consentLabel: CONSENT } })
    await nextTick()

    expect(container.querySelector('form')).toBeNull()
    expect(fetchMock).not.toHaveBeenCalled()
    expect(warnSpy.mock.calls.flat().join(' ')).toContain('bearer client')
  })

  it('speaks German, informal and formal', () => {
    const first = setup({ consentLabel: CONSENT }, { locale: 'de' })
    expect(screen.getByRole('button', { name: 'Auf die Warteliste' })).toBeVisible()
    expect(first.container.querySelector('noscript')?.textContent).toContain('schalte JavaScript')
    first.unmount()

    const second = setup({ consentLabel: CONSENT }, { locale: 'de', formality: 'formal' })
    expect(second.container.querySelector('noscript')?.textContent).toContain(
      'schalten Sie JavaScript',
    )
  })
})

describe('GovueWaitlistForm in a browser that cannot solve', () => {
  it('says so, disables the button and describes it with the reason, without a Worker', async () => {
    vi.stubGlobal('Worker', undefined)
    const { user, server } = setup()
    await nextTick()

    const text = screen.getByText(/This browser cannot run the spam check/)
    expect(join()).toBeDisabled()
    expect(join()).toHaveAccessibleDescription(text.textContent?.trim() ?? '')
    await user.click(screen.getByLabelText(/^Email address/))
    expect(server.requests).toHaveLength(0)
  })

  it('says so without crypto.subtle, which an insecure context lacks', async () => {
    vi.stubGlobal('crypto', {})
    setup()
    await nextTick()

    expect(screen.getByText(/This browser cannot run the spam check/)).toBeVisible()
    expect(join()).toBeDisabled()
  })
})

describe('GovueWaitlistForm checks before sending', () => {
  it('asks for the address and the consent, sends nothing and focuses the first error', async () => {
    const { user, server } = setup()

    await user.click(join())

    const email = screen.getByLabelText(/^Email address/)
    expect(email).toHaveAttribute('aria-invalid', 'true')
    expect(email).toHaveAccessibleDescription('Please enter your email address.')
    expect(email).toHaveFocus()
    expect(screen.getByRole('checkbox')).toHaveAccessibleDescription(
      'Please agree before you join.',
    )
    expect(server.submits).toHaveLength(0)
  })

  it('needs the consent box ticked: an address alone is not sent', async () => {
    const { user, server } = setup()
    await user.type(screen.getByLabelText(/^Email address/), 'ada@example.com')

    await user.click(join())

    const box = screen.getByRole('checkbox')
    expect(box).toHaveAttribute('aria-invalid', 'true')
    expect(box).toHaveAccessibleDescription('Please agree before you join.')
    expect(box).toHaveFocus()
    expect(server.submits).toHaveLength(0)
    expect(server.challenges.length).toBeLessThanOrEqual(1)
  })

  it('names the limit when the address is too long', async () => {
    const { user, server } = setup()
    await user.click(screen.getByLabelText(/^Email address/))
    await user.paste(`${'a'.repeat(250)}@example.com`)
    await user.click(screen.getByRole('checkbox'))

    await user.click(join())

    expect(screen.getByText('This is too long. At most 254 characters are allowed.')).toBeVisible()
    expect(server.submits).toHaveLength(0)
  })

  it('is sent with the keyboard alone', async () => {
    const { user, server } = setup()
    await user.tab()
    expect(screen.getByLabelText(/^Email address/)).toHaveFocus()
    await user.keyboard('ada@example.com')
    await user.tab()
    expect(screen.getByRole('checkbox')).toHaveFocus()
    await user.keyboard(' ')
    await user.tab()
    expect(join()).toHaveFocus()

    await user.keyboard('{Enter}')
    await settle(() => inbox() !== null)

    expect(server.submits).toHaveLength(1)
  })
})

describe('GovueWaitlistForm states', () => {
  it('goes from solving to sending to the inbox text, and focuses the status', async () => {
    const { user, server, fetchSpy, emitted } = setup()
    let answer: (response: Response) => void = () => undefined
    fetchSpy.mockImplementation((input, init) => {
      const url = typeof input === 'string' ? input : ''
      if (url.endsWith('/challenge')) return server.fetch(input, init)
      return new Promise<Response>((resolve) => {
        answer = resolve
      })
    })
    await fill(user)

    await user.click(join())
    expect(screen.getByRole('status')).toHaveTextContent('Running the spam check in your browser.')
    // While busy the button is named by its loading text as well.
    expect(screen.getByRole('button')).toBeDisabled()

    await settle(() => screen.getByRole('status').textContent?.includes('Sending') ?? false)
    expect(screen.getByRole('status')).toHaveTextContent('Sending your request')
    expect(screen.getByRole('form')).toHaveAttribute('aria-busy', 'true')
    expect(inbox()).toBeNull()

    answer(new Response('{"ok":true}', { status: 200 }))
    await settle(() => inbox() !== null)

    expect(screen.getByRole('status')).toHaveTextContent(
      'Almost done. Please check your inbox and confirm your address with the link in the mail.',
    )
    expect(screen.getByRole('status')).toHaveFocus()
    expect(screen.queryByRole('form')).toBeNull()
    expect(emitted().requested).toHaveLength(1)
  })

  it('sends exactly the keys optin knows, with no consent version, and the server accepts them', async () => {
    const { user, server } = setup()
    await fill(user)

    await user.click(join())
    await settle(() => inbox() !== null)

    expect(server.submits).toHaveLength(1)
    const post = server.submits[0]
    expect(post.url).toBe('/api/v1/waitlist')
    expect(post.headers['Content-Type']).toBe('application/json')
    expect(Object.keys(post.body as object).sort()).toEqual([
      'consent',
      'email',
      'language',
      'pow',
      'website',
    ])
    expect(post.body).toMatchObject({
      email: 'ada@example.com',
      consent: true,
      language: 'en',
      website: '',
      pow: { algorithm: 'SHA-256', number: expect.any(Number) as number },
    })
  })

  it('has a fake that refuses a consent version key, as optin refuses an unknown key', async () => {
    const server = new FakeWaitlistServer()
    const answer = await server.fetch('/api/v1/waitlist', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email: 'ada@example.com', consent: true, consentVersion: '1' }),
    })
    expect(answer.status).toBe(400)
  })

  it('sends the page language, the one the form texts use', async () => {
    const { user, server } = setup({ consentLabel: CONSENT }, { locale: 'de' })
    await user.type(screen.getByLabelText(/^E-Mail-Adresse/), 'ada@example.com')
    await user.click(screen.getByRole('checkbox'))

    await user.click(screen.getByRole('button', { name: 'Auf die Warteliste' }))
    await settle(() => server.submits.length > 0)

    expect(server.submits[0].body).toMatchObject({ language: 'de' })
  })

  it('uses the routes it is given', async () => {
    const { user, server } = setup({
      consentLabel: CONSENT,
      challengePath: '/x/challenge',
      requestPath: '/x/join',
    })
    await fill(user)

    await user.click(join())
    await settle(() => server.submits.length > 0)

    expect(server.requests.map((r) => r.url)).toEqual(['/api/v1/x/challenge', '/api/v1/x/join'])
  })

  it('waits out the minimum age before it sends', async () => {
    const { user, server } = setup()
    await fill(user)
    const started = Date.now()

    await user.click(join())
    await settle(() => server.submits.length > 0)

    expect(Date.now() - started).toBeGreaterThanOrEqual(2000)
  })

  it('answers a filled honeypot with the inbox text and sends nothing', async () => {
    const { user, server, container } = setup()
    await fill(user)
    await user.type(container.querySelector('input[name="website"]') as HTMLElement, 'x')

    await user.click(join())
    await vi.advanceTimersByTimeAsync(10)

    expect(inbox()).not.toBeNull()
    expect(server.submits).toHaveLength(0)
  })

  it('offers another address after the request and returns focus to the field', async () => {
    const { user } = setup()
    await fill(user)
    await user.click(join())
    await settle(() => screen.queryByRole('button', { name: 'Use another address' }) !== null)

    await user.click(screen.getByRole('button', { name: 'Use another address' }))

    expect(screen.getByLabelText(/^Email address/)).toHaveValue('')
    expect(screen.getByRole('checkbox')).not.toBeChecked()
    expect(screen.getByLabelText(/^Email address/)).toHaveFocus()
  })
})

describe('GovueWaitlistForm refusals', () => {
  // One row per code the Request route of optin/handler.go answers, with its
  // status and a part of the text the visitor sees.
  const refusals: Array<[number, string, string]> = [
    [403, 'cross_origin', 'The request was refused.'],
    [503, 'not_configured', 'The waitlist is not set up yet.'],
    [400, 'pow_required', 'The spam check is missing.'],
    [400, 'pow_invalid', 'The spam check failed.'],
    [400, 'pow_expired', 'The spam check ran out of time.'],
    [400, 'pow_too_fast', 'sent too fast'],
    [400, 'pow_replay', 'already used'],
    [503, 'pow_busy', 'The server is busy'],
    [503, 'mail_limit', 'reached its limit of mails'],
    [429, 'too_many_requests', 'Too many requests.'],
    [413, 'request_too_large', 'too large'],
    [415, 'unsupported_media_type', 'could not be accepted'],
    [405, 'method_not_allowed', 'could not be accepted'],
    [400, 'invalid_argument', 'could not be accepted'],
    [500, 'internal', 'Something went wrong.'],
  ]
  for (const [status, code, text] of refusals) {
    it(`shows the text of ${code} (${status}), focused, and never the server text`, async () => {
      const server = new FakeWaitlistServer()
      server.refuseNext(() => refusal(status, code))
      const { user, emitted } = setup({ consentLabel: CONSENT }, { server })
      await fill(user)

      await user.click(join())
      await settle(() => screen.queryByRole('alert') !== null)

      const alert = screen.getByRole('alert')
      expect(alert).toHaveTextContent(text)
      expect(alert).not.toHaveTextContent('English server text')
      expect(alert).toHaveFocus()
      expect(inbox()).toBeNull()
      expect(emitted().requested).toBeUndefined()
      expect(join()).toBeEnabled()
      expect(screen.getByLabelText(/^Email address/)).toHaveValue('ada@example.com')
    })
  }

  it('names the wait of mail_limit from its Retry-After', async () => {
    const server = new FakeWaitlistServer()
    server.refuseNext(() =>
      refusal(503, 'mail_limit', undefined, undefined, { 'Retry-After': '1800' }),
    )
    const { user } = setup({ consentLabel: CONSENT }, { server })
    await fill(user)

    await user.click(join())
    await settle(() => screen.queryByRole('alert') !== null)

    expect(screen.getByRole('alert')).toHaveTextContent('Please try again in about 30 minutes.')
  })

  it('puts the real field answer of the server on the address field, associated and focused', async () => {
    const { user } = setup()
    await fill(user, 'not-an-address')

    await user.click(join())
    await settle(() => screen.queryByText('This is not a valid email address.') !== null)

    const email = screen.getByLabelText(/^Email address/)
    expect(email).toHaveAttribute('aria-invalid', 'true')
    expect(email).toHaveAccessibleDescription('This is not a valid email address.')
    expect(email).toHaveFocus()
  })

  it('puts a consent refusal of the server on the box', async () => {
    const server = new FakeWaitlistServer()
    server.refuseNext(() =>
      refusal(400, 'invalid_argument', 'some fields are invalid', { consent: 'required' }),
    )
    const { user } = setup({ consentLabel: CONSENT }, { server })
    await fill(user)

    await user.click(join())
    await settle(() => screen.getByRole('checkbox').getAttribute('aria-invalid') === 'true')

    expect(screen.getByRole('checkbox')).toHaveAccessibleDescription(
      'Please agree before you join.',
    )
  })

  it('never shows the inbox text when the network is down', async () => {
    const { user, fetchSpy, server } = setup()
    fetchSpy.mockImplementation((input, init) => {
      const url = typeof input === 'string' ? input : ''
      if (url.endsWith('/challenge')) return server.fetch(input, init)
      return Promise.reject(new TypeError('Failed to fetch'))
    })
    await fill(user)

    await user.click(join())
    await settle(() => screen.queryByRole('alert') !== null)

    expect(screen.getByRole('alert')).toHaveTextContent('No connection to the server.')
    expect(inbox()).toBeNull()
  })

  it('asks for a new challenge after a refused request', async () => {
    const server = new FakeWaitlistServer()
    server.refuseNext(() => refusal(400, 'pow_replay'))
    const { user } = setup({ consentLabel: CONSENT }, { server })
    await fill(user)

    await user.click(join())
    await settle(() => screen.queryByRole('alert') !== null)
    await vi.advanceTimersByTimeAsync(0)

    expect(server.challenges).toHaveLength(2)
  })
})

describe('GovueWaitlistForm solver failures', () => {
  it('says the spam check could not run when the worker fails, and sends nothing', async () => {
    FakeWorker.mode = 'error'
    const { user, server } = setup()
    await fill(user)

    await user.click(join())
    await settle(() => screen.queryByRole('alert') !== null)

    expect(screen.getByRole('alert')).toHaveTextContent('The spam check could not run')
    expect(screen.getByRole('alert')).not.toHaveTextContent('No connection')
    expect(server.submits).toHaveLength(0)
    expect(FakeWorker.terminated).toBeGreaterThan(0)
  })

  it('gives up on a worker that never answers and leaves the solving state', async () => {
    FakeWorker.mode = 'silent'
    const { user, server } = setup()
    await fill(user)

    await user.click(join())
    expect(screen.getByRole('status')).toHaveTextContent('Running the spam check')
    await settle(() => screen.queryByRole('alert') !== null, 800)

    expect(screen.getByRole('alert')).toHaveTextContent('The spam check could not run')
    expect(screen.getByRole('status')).toHaveTextContent('')
    expect(join()).toBeEnabled()
    expect(server.submits).toHaveLength(0)
  })

  it('cancels a request on its way when the form is removed, and emits nothing', async () => {
    const { user, unmount, emitted, fetchSpy, server } = setup()
    let signal: AbortSignal | null | undefined
    let answer: (response: Response) => void = () => undefined
    fetchSpy.mockImplementation((input, init) => {
      const url = typeof input === 'string' ? input : ''
      if (url.endsWith('/challenge')) return server.fetch(input, init)
      signal = init?.signal
      return new Promise<Response>((resolve) => {
        answer = resolve
      })
    })
    await fill(user)
    await user.click(join())
    await settle(() => signal !== undefined)

    unmount()
    answer(new Response('{"ok":true}', { status: 200 }))
    await vi.advanceTimersByTimeAsync(0)

    expect(signal?.aborted).toBe(true)
    expect(emitted().requested).toBeUndefined()
  })
})

describe('GovueWaitlistForm privacy boundaries', () => {
  it('calls only the same origin and adds no header of its own', async () => {
    const { user, server, fetchSpy } = setup()
    await fill(user)
    await user.click(join())
    await settle(() => server.submits.length > 0)

    for (const [input] of fetchSpy.mock.calls) {
      const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
      expect(new URL(url, window.location.origin).origin, url).toBe(window.location.origin)
    }
    for (const request of server.requests) {
      const names = Object.keys(request.headers).map((name) => name.toLowerCase())
      expect(names.filter((n) => n !== 'accept' && n !== 'content-type')).toEqual([])
    }
  })

  it('reads and writes no cookie', async () => {
    const { user, server } = setup()
    await fill(user)
    await user.click(join())
    await settle(() => server.submits.length > 0)

    expect(cookieReads).toBe(0)
    expect(cookieWrites).toBe(0)
  })

  it('starts no worker and fetches nothing before the first focus', () => {
    const { server } = setup()
    expect(FakeWorker.created).toBe(0)
    expect(server.requests).toHaveLength(0)
  })
})
