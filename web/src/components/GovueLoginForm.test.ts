// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import userEvent from '@testing-library/user-event'
import { render, screen, waitFor } from '@testing-library/vue'
import { describe, expect, it, vi } from 'vitest'

import { createClient } from '../api/client'
import { createGovueI18n } from '../i18n/plugin'
import GovueLoginForm from './GovueLoginForm.vue'

/** answer builds a Response the fake fetch hands back. */
function answer(status: number, body?: unknown, headers: Record<string, string> = {}): Response {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', ...headers },
  })
}

function failure(status: number, code: string, message = 'An English server text.') {
  return answer(status, { error: { code, message } })
}

function setup(respond: () => Promise<Response> | Response, props: Record<string, unknown> = {}) {
  const fetchMock = vi.fn<typeof globalThis.fetch>(async () => respond())
  const client = createClient({ fetch: fetchMock })
  const view = render(GovueLoginForm, { props: { client, ...props } })
  return { fetchMock, ...view }
}

async function fill(user: ReturnType<typeof userEvent.setup>) {
  await user.type(screen.getByLabelText(/^Login/), 'alice@example.com')
  await user.type(screen.getByLabelText(/^Password/), 'a fake password for tests')
  await user.type(screen.getByLabelText(/^Code from/), '123456')
}

describe('GovueLoginForm', () => {
  it('names the form and labels every field', () => {
    setup(() => answer(200, { login: 'x' }))

    expect(screen.getByRole('form', { name: 'Log in' })).toBeVisible()
    expect(screen.getByLabelText(/^Login/)).toBeRequired()
    expect(screen.getByLabelText(/^Password/)).toHaveAttribute('type', 'password')
    expect(screen.getByLabelText(/^Password/)).toHaveAttribute('autocomplete', 'current-password')
    expect(screen.getByLabelText(/^Code from/)).toHaveAttribute('autocomplete', 'one-time-code')
    expect(screen.getByLabelText(/^Code from/)).toHaveAccessibleDescription(/Six digits/)
    expect(screen.getByRole('button', { name: 'Log in' })).toHaveAttribute('type', 'submit')
  })

  it('asks for every field before it sends anything', async () => {
    const user = userEvent.setup()
    const { fetchMock } = setup(() => answer(200, { login: 'x' }))

    await user.click(screen.getByRole('button', { name: 'Log in' }))

    expect(screen.getAllByText('Please fill in this field.')).toHaveLength(3)
    expect(screen.getByLabelText(/^Login/)).toHaveAttribute('aria-invalid', 'true')
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('posts the three fields as one JSON object and hands the answer on', async () => {
    const user = userEvent.setup()
    const { fetchMock, emitted } = setup(() =>
      answer(200, { login: 'alice@example.com', token: 't' }),
    )

    await fill(user)
    await user.click(screen.getByRole('button', { name: 'Log in' }))

    await waitFor(() => expect(emitted().success).toHaveLength(1))
    expect(emitted().success[0]).toEqual([{ login: 'alice@example.com', token: 't' }])

    const [url, init] = fetchMock.mock.calls[0]
    expect(url).toBe('/api/v1/account/login')
    expect(init?.method).toBe('POST')
    expect(JSON.parse(init?.body as string)).toEqual({
      login: 'alice@example.com',
      password: 'a fake password for tests',
      code: '123456',
    })
    // The form assumes no cookie and no token: it sends no auth header of its own.
    expect(init?.headers).not.toHaveProperty('Authorization')
  })

  it('keeps neither the password nor the code after a login', async () => {
    const user = userEvent.setup()
    const { emitted } = setup(() => answer(200, { login: 'alice@example.com' }))

    await fill(user)
    await user.click(screen.getByRole('button', { name: 'Log in' }))

    await waitFor(() => expect(emitted().success).toHaveLength(1))
    expect(screen.getByLabelText(/^Password/)).toHaveValue('')
    expect(screen.getByLabelText(/^Code from/)).toHaveValue('')
    expect(screen.getByLabelText(/^Login/)).toHaveValue('alice@example.com')
  })

  it('says one thing for a refused login and keeps what was typed', async () => {
    const user = userEvent.setup()
    const { emitted } = setup(() => failure(401, 'invalid_credentials'))

    await fill(user)
    await user.click(screen.getByRole('button', { name: 'Log in' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'The login, the password or the code is not correct.',
    )
    // The server's own English text is never shown, and no field is blamed.
    expect(screen.queryByText('An English server text.')).not.toBeInTheDocument()
    expect(screen.getByLabelText(/^Login/)).not.toHaveAttribute('aria-invalid')
    expect(screen.getByLabelText(/^Login/)).toHaveValue('alice@example.com')
    expect(screen.getByLabelText(/^Password/)).toHaveValue('a fake password for tests')
    expect(emitted().success).toBeUndefined()
  })

  it.each([
    [503, 'unavailable', 'The server is busy. Please try again in a moment.'],
    [429, 'too_many_requests', 'Too many attempts. Please wait a moment and try again.'],
    [403, 'permission_denied', 'The request was refused. Please reload the page and try again.'],
    [500, 'internal', 'Something went wrong. Please try again.'],
  ])('answers a %i in the visitor language', async (status, code, text) => {
    const user = userEvent.setup()
    setup(() => failure(status, code))

    await fill(user)
    await user.click(screen.getByRole('button', { name: 'Log in' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(text)
  })

  it('answers a dead network and shows the request id of a server failure', async () => {
    const user = userEvent.setup()
    const { fetchMock } = setup(() => Promise.reject(new TypeError('failed to fetch')))

    await fill(user)
    await user.click(screen.getByRole('button', { name: 'Log in' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(/No connection to the server/)

    fetchMock.mockResolvedValueOnce(
      answer(500, { error: { code: 'internal', message: 'internal error', request_id: 'req-42' } }),
    )
    await user.click(screen.getByRole('button', { name: 'Log in' }))
    expect(await screen.findByText('req-42')).toBeVisible()
  })

  it('is busy while the request runs, and sends it once', async () => {
    const user = userEvent.setup()
    let release: (r: Response) => void = () => {}
    const { fetchMock } = setup(
      () =>
        new Promise<Response>((resolve) => {
          release = resolve
        }),
    )

    await fill(user)
    await user.click(screen.getByRole('button', { name: 'Log in' }))

    const button = await screen.findByRole('button', { name: /Working/ })
    expect(button).toBeDisabled()
    expect(button).toHaveAttribute('aria-busy', 'true')
    expect(screen.getByLabelText(/^Login/)).toBeDisabled()
    expect(screen.getByLabelText(/^Password/)).toBeDisabled()

    // A second submit while the first runs sends nothing.
    await user.keyboard('{Enter}')
    expect(fetchMock).toHaveBeenCalledTimes(1)

    release(answer(200, { login: 'alice@example.com' }))
    await waitFor(() => expect(screen.getByLabelText(/^Login/)).toBeEnabled())
  })

  it('is disabled as a whole when asked to', () => {
    setup(() => answer(200, { login: 'x' }), { disabled: true })

    expect(screen.getByLabelText(/^Login/)).toBeDisabled()
    expect(screen.getByLabelText(/^Password/)).toBeDisabled()
    expect(screen.getByLabelText(/^Code from/)).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Log in' })).toBeDisabled()
  })

  it('posts to the path it is given', async () => {
    const user = userEvent.setup()
    const { fetchMock } = setup(() => answer(200, { login: 'x' }), { path: '/auth/in' })

    await fill(user)
    await user.click(screen.getByRole('button', { name: 'Log in' }))

    await waitFor(() => expect(fetchMock).toHaveBeenCalled())
    expect(fetchMock.mock.calls[0][0]).toBe('/api/v1/auth/in')
  })

  it('speaks German when the language layer says so', () => {
    const client = createClient({ fetch: vi.fn() as unknown as typeof globalThis.fetch })
    render(GovueLoginForm, {
      props: { client },
      global: { plugins: [createGovueI18n({ locale: 'de', detect: false })] },
    })

    expect(screen.getByRole('form', { name: 'Anmelden' })).toBeVisible()
    expect(screen.getByLabelText(/^Passwort/)).toBeVisible()
    expect(screen.getByLabelText(/^Code aus/)).toBeVisible()
  })
})
