// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import userEvent from '@testing-library/user-event'
import { render, screen, waitFor } from '@testing-library/vue'
import { nextTick } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { createClient } from '../api/client'
import { createGovueI18n } from '../i18n/plugin'
import GovuePasskeyButton from './GovuePasskeyButton.vue'

const CHALLENGE = 'FAKE-CHALLENGE-VALUE-0002'
const PASSKEY = {
  id: 'fake-id',
  name: 'Laptop',
  created_at: '2026-11-18T10:00:00Z',
  last_used_at: null,
}

function answer(status: number, body?: unknown): Response {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function installBrowser() {
  const credential: { toJSON: () => unknown } = {
    toJSON: () => ({ id: 'fake-credential-id', type: 'public-key' }),
  }
  class FakePublicKeyCredential {
    static parseCreationOptionsFromJSON = vi.fn(() => ({}))
    static parseRequestOptionsFromJSON = vi.fn(() => ({}))
  }
  vi.stubGlobal('PublicKeyCredential', FakePublicKeyCredential)
  const create = vi.fn(() => Promise.resolve(credential))
  const get = vi.fn(() => Promise.resolve(credential))
  Object.defineProperty(navigator, 'credentials', { value: { create, get }, configurable: true })
  return { create, get }
}

/** setup renders and waits one tick: the button appears after mount. */
async function setup(
  routes: Record<string, () => Response | Promise<Response>>,
  props: { mode: 'login' | 'enrol'; name?: string; disabled?: boolean },
  global: Record<string, unknown> = {},
) {
  const fetchMock = vi.fn<typeof globalThis.fetch>((input, init) => {
    const url = typeof input === 'string' ? input : ''
    const route = routes[`${init?.method} ${url.replace('/api/v1', '')}`]
    if (!route) {
      return Promise.reject(new Error(`unexpected request ${init?.method} ${url}`))
    }
    return Promise.resolve(route())
  })
  const client = createClient({ fetch: fetchMock })
  const view = render(GovuePasskeyButton, { props: { client, ...props }, global })
  await nextTick()
  return { fetchMock, ...view }
}

const routes = {
  'POST /account/passkeys/register/begin': () =>
    answer(200, { public_key: { challenge: CHALLENGE } }),
  'POST /account/passkeys/register/finish': () => answer(201, { passkey: PASSKEY }),
  'POST /account/passkeys/login/begin': () => answer(200, { public_key: { challenge: CHALLENGE } }),
  'POST /account/passkeys/login/finish': () => answer(200, { login: 'person@example.com' }),
}

let browser: ReturnType<typeof installBrowser>

beforeEach(() => {
  browser = installBrowser()
})

afterEach(() => {
  vi.unstubAllGlobals()
  Object.defineProperty(navigator, 'credentials', { value: undefined, configurable: true })
})

describe('GovuePasskeyButton', () => {
  it('is a named button for each mode', async () => {
    await setup(routes, { mode: 'login' })
    expect(screen.getByRole('button', { name: 'Log in with a passkey' })).toHaveAttribute(
      'type',
      'button',
    )
  })

  it('names the enrol button for the enrolment', async () => {
    await setup(routes, { mode: 'enrol' })
    expect(screen.getByRole('button', { name: 'Add a passkey' })).toBeVisible()
  })

  it('shows the German text under the German catalogue', async () => {
    const { install } = createGovueI18n({
      locale: 'de',
      storage: { kind: 'custom', get: () => undefined, set: () => undefined },
    })
    await setup(routes, { mode: 'login' }, { plugins: [{ install }] })
    expect(screen.getByRole('button', { name: 'Mit Passkey anmelden' })).toBeVisible()
  })

  it('hides itself where the browser lacks the helpers', async () => {
    vi.stubGlobal('PublicKeyCredential', undefined)
    const { container } = await setup(routes, { mode: 'login' })

    expect(screen.queryByRole('button')).not.toBeInTheDocument()
    expect(container.querySelector('[aria-live]')).toBeNull()
  })

  it('enrols with the name and emits the passkey', async () => {
    const user = userEvent.setup()
    const { fetchMock, emitted } = await setup(routes, { mode: 'enrol', name: 'Laptop' })

    await user.click(screen.getByRole('button', { name: 'Add a passkey' }))

    await waitFor(() => expect(emitted().success).toHaveLength(1))
    expect(emitted().success[0]).toEqual([PASSKEY])
    const finish = fetchMock.mock.calls[1]
    expect(finish[0]).toBe('/api/v1/account/passkeys/register/finish')
    expect(JSON.parse(finish[1]?.body as string)).toMatchObject({ name: 'Laptop' })
  })

  it('logs in and emits the answer', async () => {
    const user = userEvent.setup()
    const { emitted } = await setup(routes, { mode: 'login' })

    await user.click(screen.getByRole('button', { name: 'Log in with a passkey' }))

    await waitFor(() => expect(emitted().success).toHaveLength(1))
    expect(emitted().success[0]).toEqual([{ login: 'person@example.com' }])
    expect(browser.get).toHaveBeenCalledTimes(1)
  })

  it('is busy while the ceremony runs and announces it', async () => {
    const user = userEvent.setup()
    let release: () => void = () => undefined
    const gate = new Promise<void>((resolve) => {
      release = resolve
    })
    browser.get.mockImplementationOnce(async () => {
      await gate
      return { toJSON: () => ({}) }
    })
    const { emitted } = await setup(routes, { mode: 'login' })

    await user.click(screen.getByRole('button', { name: /Log in with a passkey/ }))

    const button = await screen.findByRole('button', { name: /Log in with a passkey/ })
    await waitFor(() => expect(button).toBeDisabled())
    expect(button).toHaveAttribute('aria-busy', 'true')
    expect(document.querySelector('[aria-live="polite"]')).toHaveTextContent(
      'Waiting for your passkey',
    )
    // Announced once: the live region has it, the button's own text does not.
    expect(screen.getAllByText('Waiting for your passkey')).toHaveLength(1)
    // And no second text: GovueButton's default loading text is not rendered.
    expect(screen.queryByText('Working')).not.toBeInTheDocument()
    expect(button).toHaveAccessibleName('Log in with a passkey')
    // A second click on the disabled button starts nothing.
    await user.click(button)
    expect(browser.get).toHaveBeenCalledTimes(1)

    release()
    await waitFor(() => expect(emitted().success).toHaveLength(1))
    expect(button).toBeEnabled()
  })

  it('treats a dismissed prompt as a quiet state', async () => {
    const user = userEvent.setup()
    browser.get.mockRejectedValueOnce(new DOMException(`text ${CHALLENGE}`, 'NotAllowedError'))
    const { emitted } = await setup(routes, { mode: 'login' })

    await user.click(screen.getByRole('button', { name: 'Log in with a passkey' }))

    expect(await screen.findByText(/prompt was closed/)).toBeVisible()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(emitted().cancelled).toHaveLength(1)
    expect(emitted().success).toBeUndefined()
    expect(emitted().error).toBeUndefined()
    expect(screen.getByRole('button', { name: 'Log in with a passkey' })).toBeEnabled()
  })

  it('shows a refusal in the live region in the visitor language and emits the error', async () => {
    const user = userEvent.setup()
    const { emitted } = await setup(
      {
        ...routes,
        'POST /account/passkeys/login/finish': () =>
          answer(401, { error: { code: 'invalid_credentials', message: `server ${CHALLENGE}` } }),
      },
      { mode: 'login' },
    )

    await user.click(screen.getByRole('button', { name: 'Log in with a passkey' }))

    const text = await screen.findByText('The passkey could not be verified.')
    // A failure is an alert, as in the account forms.
    expect(text.closest('[role="alert"]')).not.toBeNull()
    expect(document.body).not.toHaveTextContent(CHALLENGE)
    expect(emitted().error).toHaveLength(1)
    expect(emitted().success).toBeUndefined()
  })

  it('says to log in again when the login is too old', async () => {
    const user = userEvent.setup()
    await setup(
      {
        ...routes,
        'POST /account/passkeys/register/begin': () =>
          answer(403, { error: { code: 'reauthentication_required', message: 'x' } }),
      },
      { mode: 'enrol' },
    )

    await user.click(screen.getByRole('button', { name: 'Add a passkey' }))

    expect(await screen.findByText('Please log in again, then try once more.')).toBeVisible()
  })

  it('shows a network failure', async () => {
    const user = userEvent.setup()
    const fetchMock = vi.fn<typeof globalThis.fetch>(() =>
      Promise.reject(new TypeError('Failed to fetch')),
    )
    render(GovuePasskeyButton, {
      props: { client: createClient({ fetch: fetchMock }), mode: 'login' },
    })
    await nextTick()

    await user.click(screen.getByRole('button', { name: 'Log in with a passkey' }))

    expect(await screen.findByText(/No connection to the server/)).toBeVisible()
  })

  it('does not run when disabled', async () => {
    const user = userEvent.setup()
    const { fetchMock } = await setup(routes, { mode: 'login', disabled: true })

    await user.click(screen.getByRole('button', { name: 'Log in with a passkey' }))

    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('shows no old error text after a retry went through', async () => {
    const user = userEvent.setup()
    let refuse = true
    await setup(
      {
        ...routes,
        'POST /account/passkeys/login/finish': () =>
          refuse
            ? answer(401, { error: { code: 'invalid_credentials', message: 'x' } })
            : answer(200, { login: 'person@example.com' }),
      },
      { mode: 'login' },
    )

    await user.click(screen.getByRole('button', { name: 'Log in with a passkey' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('could not be verified')
    refuse = false
    await user.click(screen.getByRole('button', { name: 'Log in with a passkey' }))

    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument())
  })

  it('is not offered to a client with a device token', async () => {
    const fetchMock = vi.fn<typeof globalThis.fetch>()
    const client = createClient({ fetch: fetchMock, auth: { mode: 'bearer' } })
    render(GovuePasskeyButton, { props: { client, mode: 'login' } })
    await nextTick()

    expect(screen.queryByRole('button')).not.toBeInTheDocument()
  })

  it('gives the focus back to the button when the ceremony is over', async () => {
    const user = userEvent.setup()
    let release: () => void = () => undefined
    const held = new Promise<void>((resolve) => {
      release = resolve
    })
    browser.get.mockImplementationOnce(async () => {
      await held
      return { toJSON: () => ({}) }
    })
    const { emitted } = await setup(routes, { mode: 'login' })
    const button = screen.getByRole('button', { name: 'Log in with a passkey' })

    await user.click(button)
    await waitFor(() => expect(button).toBeDisabled())
    // A browser drops the focus of a button that turns disabled.
    button.blur()
    expect(document.body).toHaveFocus()
    release()
    await waitFor(() => expect(emitted().success).toHaveLength(1))

    await waitFor(() => expect(button).toHaveFocus())
  })
})
