// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import userEvent from '@testing-library/user-event'
import { render, screen, waitFor } from '@testing-library/vue'
import { describe, expect, it, vi } from 'vitest'

import { createClient } from '../api/client'
import { createGovueI18n } from '../i18n/plugin'
import GovueSignupForm from './GovueSignupForm.vue'

function answer(status: number, body?: unknown): Response {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function setup(respond: () => Promise<Response> | Response, props: Record<string, unknown> = {}) {
  const fetchMock = vi.fn<typeof globalThis.fetch>(async () => respond())
  const client = createClient({ fetch: fetchMock })
  const view = render(GovueSignupForm, { props: { client, ...props } })
  return { fetchMock, ...view }
}

const created = {
  login: 'alice@example.com',
  enrolment: {
    secret: 'ABCDEFGHIJKLMNOP',
    uri: 'otpauth://totp/Example:alice?secret=ABCDEFGHIJKLMNOP',
  },
}

async function fill(
  user: ReturnType<typeof userEvent.setup>,
  password = 'a fake password for tests',
) {
  await user.type(screen.getByLabelText(/^Login/), 'alice@example.com')
  await user.type(screen.getByLabelText(/^Password/), password)
}

describe('GovueSignupForm', () => {
  it('names the form, labels the fields and says how long a password is', () => {
    setup(() => answer(201, created))

    expect(screen.getByRole('form', { name: 'Create an account' })).toBeVisible()
    expect(screen.getByLabelText(/^Login/)).toBeRequired()
    expect(screen.getByLabelText(/^Password/)).toHaveAttribute('autocomplete', 'new-password')
    expect(screen.getByLabelText(/^Password/)).toHaveAttribute('type', 'password')
    expect(screen.getByLabelText(/^Password/)).toHaveAccessibleDescription(
      'At least 12 characters.',
    )
  })

  it('asks for both fields before it sends anything', async () => {
    const user = userEvent.setup()
    const { fetchMock } = setup(() => answer(201, created))

    await user.click(screen.getByRole('button', { name: 'Create account' }))

    expect(screen.getAllByText('Please fill in this field.')).toHaveLength(2)
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('refuses a short password without a request, counting characters', async () => {
    const user = userEvent.setup()
    const { fetchMock } = setup(() => answer(201, created))

    // Eleven characters, though more than eleven bytes.
    await fill(user, 'äöüäöüäöüäö')
    await user.click(screen.getByRole('button', { name: 'Create account' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'The password needs at least 12 characters.',
    )
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('posts login and password, hands the enrolment on and clears the password', async () => {
    const user = userEvent.setup()
    const { fetchMock, emitted } = setup(() => answer(201, created))

    await fill(user)
    await user.click(screen.getByRole('button', { name: 'Create account' }))

    await waitFor(() => expect(emitted().signedUp).toHaveLength(1))
    expect(emitted().signedUp[0]).toEqual([created])
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/v1/account/signup')
    expect(JSON.parse(init?.body as string)).toEqual({
      login: 'alice@example.com',
      password: 'a fake password for tests',
    })
    expect(screen.getByLabelText(/^Password/)).toHaveValue('')
  })

  it('sends the password exactly as typed, spaces included', async () => {
    const user = userEvent.setup()
    const { fetchMock } = setup(() => answer(201, created))

    await fill(user, '  a spaced password  ')
    await user.click(screen.getByRole('button', { name: 'Create account' }))

    await waitFor(() => expect(fetchMock).toHaveBeenCalled())
    expect(JSON.parse(fetchMock.mock.calls[0][1]?.body as string).password).toBe(
      '  a spaced password  ',
    )
  })

  it('says a refused signup in its own words', async () => {
    const user = userEvent.setup()
    setup(() =>
      answer(403, { error: { code: 'permission_denied', message: 'signup is not available' } }),
    )

    await fill(user)
    await user.click(screen.getByRole('button', { name: 'Create account' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Signup is not open, or the request was refused.',
    )
  })

  it('does not report a refused signup as a lost session', async () => {
    const user = userEvent.setup()
    const onUnauthenticated = vi.fn()
    const fetchMock = vi
      .fn<typeof globalThis.fetch>()
      .mockResolvedValue(answer(401, { error: { code: 'unauthenticated', message: 'x' } }))
    const client = createClient({ fetch: fetchMock, onUnauthenticated })
    render(GovueSignupForm, { props: { client } })

    await fill(user)
    await user.click(screen.getByRole('button', { name: 'Create account' }))

    expect(await screen.findByRole('alert')).toBeVisible()
    expect(onUnauthenticated).not.toHaveBeenCalled()
  })

  it('puts a taken login on the login field', async () => {
    const user = userEvent.setup()
    const { emitted } = setup(() =>
      answer(409, { error: { code: 'conflict', message: 'this login is already taken' } }),
    )

    await fill(user)
    await user.click(screen.getByRole('button', { name: 'Create account' }))

    expect(await screen.findByText('This login is already taken.')).toBeVisible()
    expect(screen.getByLabelText(/^Login/)).toHaveAttribute('aria-invalid', 'true')
    expect(emitted().signedUp).toBeUndefined()
  })

  it('puts the server field details on the fields, in the visitor language', async () => {
    const user = userEvent.setup()
    setup(() =>
      answer(400, {
        error: {
          code: 'invalid_argument',
          message: 'the login is not valid',
          details: { login: 'invalid' },
        },
      }),
    )

    await fill(user)
    await user.click(screen.getByRole('button', { name: 'Create account' }))

    expect(await screen.findByText('This login is not valid.')).toBeVisible()
    expect(screen.getByLabelText(/^Login/)).toHaveAttribute('aria-invalid', 'true')
  })

  it('is busy while the request runs and disabled when asked to', async () => {
    const user = userEvent.setup()
    let release: (r: Response) => void = () => {}
    setup(
      () =>
        new Promise<Response>((resolve) => {
          release = resolve
        }),
    )

    await fill(user)
    await user.click(screen.getByRole('button', { name: 'Create account' }))
    const button = await screen.findByRole('button', { name: /Working/ })
    expect(button).toBeDisabled()
    expect(screen.getByLabelText(/^Login/)).toBeDisabled()

    release(answer(201, created))
    await waitFor(() => expect(screen.getByLabelText(/^Login/)).toBeEnabled())
  })

  it('is disabled as a whole when asked to', () => {
    setup(() => answer(201, created), { disabled: true })

    expect(screen.getByLabelText(/^Login/)).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Create account' })).toBeDisabled()
  })

  it('answers a refused request in the visitor language', async () => {
    const user = userEvent.setup()
    setup(() => answer(503, { error: { code: 'unavailable', message: 'the server is busy' } }))

    await fill(user)
    await user.click(screen.getByRole('button', { name: 'Create account' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('The server is busy.')
  })

  it('speaks German when the language layer says so', () => {
    const client = createClient({ fetch: vi.fn() as unknown as typeof globalThis.fetch })
    render(GovueSignupForm, {
      props: { client },
      global: { plugins: [createGovueI18n({ locale: 'de', detect: false })] },
    })

    expect(screen.getByRole('form', { name: 'Konto erstellen' })).toBeVisible()
    expect(screen.getByLabelText(/^Passwort/)).toHaveAccessibleDescription('Mindestens 12 Zeichen.')
  })
})
