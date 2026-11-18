// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import userEvent from '@testing-library/user-event'
import { render, screen, waitFor } from '@testing-library/vue'
import { describe, expect, it, vi } from 'vitest'

import { createClient } from '../api/client'
import { createGovueI18n } from '../i18n/plugin'
import GovueTotpEnrolment from './GovueTotpEnrolment.vue'

function answer(status: number, body?: unknown): Response {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

const enrolment = {
  secret: 'ABCDEFGHIJKLMNOP',
  uri: 'otpauth://totp/Example:alice?secret=ABCDEFGHIJKLMNOP&issuer=Example',
}

function setup(respond: () => Promise<Response> | Response, props: Record<string, unknown> = {}) {
  const fetchMock = vi.fn<typeof globalThis.fetch>(async () => respond())
  const client = createClient({ fetch: fetchMock })
  const view = render(GovueTotpEnrolment, {
    props: { client, login: 'alice@example.com', enrolment, ...props },
  })
  return { fetchMock, ...view }
}

async function fill(user: ReturnType<typeof userEvent.setup>) {
  await user.type(screen.getByLabelText(/^Password/), 'a fake password for tests')
  await user.type(screen.getByLabelText(/^Code from/), '123456')
}

describe('GovueTotpEnrolment', () => {
  it('shows the secret and the provisioning URI, where they can be copied', () => {
    setup(() => answer(200, { login: 'alice@example.com' }))

    const secret = screen.getByLabelText('Secret')
    expect(secret).toHaveValue(enrolment.secret)
    expect(secret).toHaveAttribute('readonly')
    const uri = screen.getByLabelText('Provisioning link')
    expect(uri).toHaveValue(enrolment.uri)
    expect(uri).toHaveAttribute('readonly')
    expect(screen.getByText(/shown only once/)).toBeVisible()
  })

  it('links an otpauth URI to the authenticator app and nothing else', () => {
    const { unmount } = setup(() => answer(200, { login: 'x' }))
    expect(screen.getByRole('link', { name: 'Open in authenticator app' })).toHaveAttribute(
      'href',
      enrolment.uri,
    )
    unmount()

    setup(() => answer(200, { login: 'x' }), {
      enrolment: { secret: enrolment.secret, uri: 'javascript:alert(1)' },
    })
    expect(screen.queryByRole('link')).not.toBeInTheDocument()
  })

  it('does not turn a URI that merely contains otpauth:// into a link', () => {
    setup(() => answer(200, { login: 'x' }), {
      enrolment: { secret: enrolment.secret, uri: 'javascript:alert(1)//otpauth://' },
    })

    expect(screen.queryByRole('link')).not.toBeInTheDocument()
  })

  it('holds the login in a hidden username input for a password manager', () => {
    const { container } = setup(() => answer(200, { login: 'x' }))

    const hidden = container.querySelector('input[autocomplete="username"]')
    expect(hidden).toHaveValue('alice@example.com')
    expect(hidden).toHaveAttribute('aria-hidden', 'true')
    expect(hidden).toHaveAttribute('tabindex', '-1')
    // The field a person fills in is not it.
    expect(screen.queryByLabelText(/alice@example.com/)).not.toBeInTheDocument()
  })

  it('does not report a refused confirmation as a lost session', async () => {
    const user = userEvent.setup()
    const onUnauthenticated = vi.fn()
    const fetchMock = vi
      .fn<typeof globalThis.fetch>()
      .mockResolvedValue(answer(401, { error: { code: 'invalid_credentials', message: 'x' } }))
    const client = createClient({ fetch: fetchMock, onUnauthenticated })
    render(GovueTotpEnrolment, { props: { client, login: 'alice@example.com', enrolment } })

    await fill(user)
    await user.click(screen.getByRole('button', { name: 'Confirm code' }))

    expect(await screen.findByRole('alert')).toBeVisible()
    expect(onUnauthenticated).not.toHaveBeenCalled()
  })

  it('draws no QR code', () => {
    const { container } = setup(() => answer(200, { login: 'x' }))

    expect(container.querySelector('canvas, img, svg[role="img"]')).toBeNull()
  })

  it('names the form and asks for the password and the code', () => {
    setup(() => answer(200, { login: 'x' }))

    expect(screen.getByRole('form', { name: 'Set up your authenticator app' })).toBeVisible()
    expect(screen.getByLabelText(/^Password/)).toBeRequired()
    expect(screen.getByLabelText(/^Password/)).toHaveAttribute('type', 'password')
    expect(screen.getByLabelText(/^Code from/)).toBeRequired()
    expect(screen.getByLabelText(/^Code from/)).toHaveAttribute('inputmode', 'numeric')
  })

  it('asks for both fields before it sends anything', async () => {
    const user = userEvent.setup()
    const { fetchMock } = setup(() => answer(200, { login: 'x' }))

    await user.click(screen.getByRole('button', { name: 'Confirm code' }))

    expect(screen.getAllByText('Please fill in this field.')).toHaveLength(2)
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('confirms with the login it was given and hands the answer on', async () => {
    const user = userEvent.setup()
    const { fetchMock, emitted } = setup(() => answer(200, { login: 'alice@example.com' }))

    await fill(user)
    await user.click(screen.getByRole('button', { name: 'Confirm code' }))

    await waitFor(() => expect(emitted().confirmed).toHaveLength(1))
    expect(emitted().confirmed[0]).toEqual([{ login: 'alice@example.com' }])
    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/v1/account/confirm')
    expect(JSON.parse(init?.body as string)).toEqual({
      login: 'alice@example.com',
      password: 'a fake password for tests',
      code: '123456',
    })
    expect(screen.getByLabelText(/^Password/)).toHaveValue('')
    expect(screen.getByLabelText(/^Code from/)).toHaveValue('')
  })

  it('says one thing for a refused confirmation', async () => {
    const user = userEvent.setup()
    const { emitted } = setup(() =>
      answer(401, { error: { code: 'invalid_credentials', message: 'English text' } }),
    )

    await fill(user)
    await user.click(screen.getByRole('button', { name: 'Confirm code' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'The login, the password or the code is not correct.',
    )
    expect(emitted().confirmed).toBeUndefined()
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
    await user.click(screen.getByRole('button', { name: 'Confirm code' }))
    expect(await screen.findByRole('button', { name: /Working/ })).toBeDisabled()
    expect(screen.getByLabelText(/^Code from/)).toBeDisabled()

    release(answer(200, { login: 'alice@example.com' }))
    await waitFor(() => expect(screen.getByLabelText(/^Code from/)).toBeEnabled())
  })

  it('is disabled as a whole when asked to', () => {
    setup(() => answer(200, { login: 'x' }), { disabled: true })

    expect(screen.getByLabelText(/^Password/)).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Confirm code' })).toBeDisabled()
  })

  it('speaks German when the language layer says so', () => {
    const client = createClient({ fetch: vi.fn() as unknown as typeof globalThis.fetch })
    render(GovueTotpEnrolment, {
      props: { client, login: 'alice@example.com', enrolment },
      global: { plugins: [createGovueI18n({ locale: 'de', detect: false })] },
    })

    expect(screen.getByRole('form', { name: 'Authenticator-App einrichten' })).toBeVisible()
    expect(screen.getByLabelText('Geheimnis')).toHaveValue(enrolment.secret)
    expect(screen.getByRole('button', { name: 'Code bestätigen' })).toBeVisible()
  })
})
