// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest'

import { createClient } from '../api/client'
import { isPasskeySupported, PasskeyError, usePasskey } from './usePasskey'

// The fake challenge and ids are test values and never real.
const CHALLENGE = 'FAKE-CHALLENGE-VALUE-0001'
const CREDENTIAL_ID = 'fake-credential-id'
const ATTESTATION = 'fake-attestation-object'

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

function failure(status: number, code: string, message = `server text ${CHALLENGE}`): Response {
  return answer(status, {
    error: { code, message, details: { leak: CHALLENGE }, request_id: 'r1' },
  })
}

function domError(name: string, message = `browser text ${CHALLENGE}`): DOMException {
  return new DOMException(message, name)
}

interface Stubs {
  create: ReturnType<typeof vi.fn>
  get: Mock<(args: { signal: AbortSignal }) => Promise<unknown>>
  parseCreation: ReturnType<typeof vi.fn>
  parseRequest: ReturnType<typeof vi.fn>
  credentials: { toJSON: ReturnType<typeof vi.fn> }
}

function installBrowser(): Stubs {
  const credential = {
    toJSON: vi.fn(() => ({
      id: CREDENTIAL_ID,
      rawId: CREDENTIAL_ID,
      type: 'public-key',
      response: { attestationObject: ATTESTATION },
    })),
  }
  const parseCreation = vi.fn((json: { challenge: string }) => ({ parsed: 'creation', json }))
  const parseRequest = vi.fn((json: { challenge: string }) => ({ parsed: 'request', json }))
  class FakePublicKeyCredential {
    static parseCreationOptionsFromJSON = parseCreation
    static parseRequestOptionsFromJSON = parseRequest
  }
  vi.stubGlobal('PublicKeyCredential', FakePublicKeyCredential)
  const create = vi.fn(() => Promise.resolve(credential))
  const get = vi.fn(() => Promise.resolve(credential))
  Object.defineProperty(navigator, 'credentials', {
    value: { create, get },
    configurable: true,
  })
  return { create, get, parseCreation, parseRequest, credentials: credential }
}

function removeNavigatorCredentials() {
  Object.defineProperty(navigator, 'credentials', { value: undefined, configurable: true })
}

type Route = (body: unknown, init: RequestInit | undefined) => Response | Promise<Response>

/** A fake server keyed by "METHOD path". */
function setup(routes: Record<string, Route>, clientOptions: Record<string, unknown> = {}) {
  const fetchMock = vi.fn<typeof globalThis.fetch>(async (input, init) => {
    const path = (typeof input === 'string' ? input : '').replace('/api/v1', '')
    const route = routes[`${init?.method} ${path}`]
    if (!route) {
      throw new Error(`unexpected request ${init?.method} ${path}`)
    }
    const body = typeof init?.body === 'string' ? JSON.parse(init.body) : undefined
    return await route(body, init)
  })
  const client = createClient({ fetch: fetchMock, ...clientOptions })
  return { fetchMock, client, passkey: usePasskey({ client }) }
}

const happy: Record<string, Route> = {
  'POST /account/passkeys/register/begin': () =>
    answer(200, { public_key: { challenge: CHALLENGE } }),
  'POST /account/passkeys/register/finish': () => answer(201, { passkey: PASSKEY }),
  'POST /account/passkeys/login/begin': () => answer(200, { public_key: { challenge: CHALLENGE } }),
  'POST /account/passkeys/login/finish': () => answer(200, { login: 'person@example.com' }),
}

let stubs: Stubs

beforeEach(() => {
  stubs = installBrowser()
})

afterEach(() => {
  vi.unstubAllGlobals()
  removeNavigatorCredentials()
})

describe('isPasskeySupported', () => {
  it('is true with the helpers and navigator.credentials', () => {
    expect(isPasskeySupported()).toBe(true)
  })

  it('is false without PublicKeyCredential', () => {
    vi.stubGlobal('PublicKeyCredential', undefined)
    expect(isPasskeySupported()).toBe(false)
  })

  it('is false when a native JSON helper is missing', () => {
    class Old {
      static parseCreationOptionsFromJSON = vi.fn()
    }
    vi.stubGlobal('PublicKeyCredential', Old)
    expect(isPasskeySupported()).toBe(false)
  })

  it('is false without navigator.credentials', () => {
    removeNavigatorCredentials()
    expect(isPasskeySupported()).toBe(false)
  })
})

describe('enrol', () => {
  it('runs begin, the browser and finish with the bodies of the contract', async () => {
    const { passkey, fetchMock } = setup(happy)

    const result = await passkey.enrol('Laptop')

    expect(result).toEqual(PASSKEY)
    expect(passkey.status.value).toBe('done')
    expect(passkey.error.value).toBeUndefined()
    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      '/api/v1/account/passkeys/register/begin',
      '/api/v1/account/passkeys/register/finish',
    ])
    expect(JSON.parse(fetchMock.mock.calls[0][1]?.body as string)).toEqual({})
    expect(fetchMock.mock.calls[0][1]?.method).toBe('POST')
    expect(stubs.parseCreation).toHaveBeenCalledWith({ challenge: CHALLENGE })
    expect(stubs.create).toHaveBeenCalledWith({
      publicKey: { parsed: 'creation', json: { challenge: CHALLENGE } },
    })
    expect(JSON.parse(fetchMock.mock.calls[1][1]?.body as string)).toEqual({
      credential: {
        id: CREDENTIAL_ID,
        rawId: CREDENTIAL_ID,
        type: 'public-key',
        response: { attestationObject: ATTESTATION },
      },
      name: 'Laptop',
    })
  })

  it('sends no name field when none is given', async () => {
    const { passkey, fetchMock } = setup(happy)

    await passkey.enrol()

    const body = JSON.parse(fetchMock.mock.calls[1][1]?.body as string)
    expect(Object.keys(body)).toEqual(['credential'])
  })

  it('uses the credentials mode of the client it was given', async () => {
    const { passkey, fetchMock } = setup(happy, { credentials: 'include' })

    await passkey.enrol()

    for (const [, init] of fetchMock.mock.calls) {
      expect(init?.credentials).toBe('include')
    }
  })

  it('lets the client report a lost session on enrolment', async () => {
    const onUnauthenticated = vi.fn()
    const { passkey } = setup(
      { 'POST /account/passkeys/register/begin': () => failure(401, 'unauthenticated') },
      { onUnauthenticated },
    )

    await expect(passkey.enrol()).rejects.toMatchObject({ code: 'unauthenticated' })
    expect(onUnauthenticated).toHaveBeenCalledTimes(1)
  })

  it('reports an excluded authenticator as already registered', async () => {
    stubs.create.mockRejectedValueOnce(domError('InvalidStateError'))
    const { passkey } = setup(happy)

    await expect(passkey.enrol()).rejects.toMatchObject({ code: 'already_registered' })
  })

  it('reports another browser failure as failed', async () => {
    stubs.create.mockRejectedValueOnce(domError('SecurityError'))
    const { passkey } = setup(happy)

    await expect(passkey.enrol()).rejects.toMatchObject({ code: 'failed' })
  })

  it('reports a credential without toJSON as unsupported', async () => {
    stubs.create.mockResolvedValueOnce({})
    const { passkey, fetchMock } = setup(happy)

    await expect(passkey.enrol()).rejects.toMatchObject({ code: 'unsupported' })
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('reports an answer without a passkey as failed', async () => {
    const { passkey } = setup({
      ...happy,
      'POST /account/passkeys/register/finish': () => answer(201, {}),
    })

    await expect(passkey.enrol()).rejects.toMatchObject({ code: 'failed' })
  })
})

describe('login', () => {
  it('runs begin, the browser and finish with the bodies of the contract', async () => {
    const { passkey, fetchMock } = setup(happy)

    const result = await passkey.login()

    expect(result).toEqual({ login: 'person@example.com' })
    expect(fetchMock.mock.calls.map(([url]) => url)).toEqual([
      '/api/v1/account/passkeys/login/begin',
      '/api/v1/account/passkeys/login/finish',
    ])
    expect(JSON.parse(fetchMock.mock.calls[0][1]?.body as string)).toEqual({})
    expect(stubs.parseRequest).toHaveBeenCalledWith({ challenge: CHALLENGE })
    const getArgs = stubs.get.mock.calls[0][0] as Record<string, unknown>
    expect(getArgs.publicKey).toEqual({ parsed: 'request', json: { challenge: CHALLENGE } })
    expect(getArgs).not.toHaveProperty('mediation')
    expect(JSON.parse(fetchMock.mock.calls[1][1]?.body as string)).toEqual({
      credential: {
        id: CREDENTIAL_ID,
        rawId: CREDENTIAL_ID,
        type: 'public-key',
        response: { attestationObject: ATTESTATION },
      },
    })
  })

  it('asks for conditional mediation on request and leaves the status alone', async () => {
    const { passkey } = setup(happy)

    const result = await passkey.login({ conditional: true })

    expect(result).toEqual({ login: 'person@example.com' })
    expect(stubs.get.mock.calls[0][0]).toMatchObject({ mediation: 'conditional' })
    expect(passkey.status.value).toBe('idle')
  })

  it('does not report a refused login as a lost session', async () => {
    const onUnauthenticated = vi.fn()
    const { passkey } = setup(
      {
        ...happy,
        'POST /account/passkeys/login/finish': () => failure(401, 'invalid_credentials'),
      },
      { onUnauthenticated },
    )

    await expect(passkey.login()).rejects.toMatchObject({ code: 'invalid_credentials' })
    expect(onUnauthenticated).not.toHaveBeenCalled()
  })

  it('ends a waiting conditional ceremony when a prompt is asked for', async () => {
    let seen: AbortSignal | undefined
    stubs.get.mockImplementationOnce(
      (args: { signal: AbortSignal }) =>
        new Promise((_resolve, reject) => {
          seen = args.signal
          args.signal.addEventListener('abort', () => {
            reject(domError('AbortError'))
          })
        }),
    )
    const { passkey } = setup(happy)

    const waiting = passkey.login({ conditional: true })
    await vi.waitFor(() => expect(seen).toBeDefined())
    const prompted = await passkey.login()

    expect(await waiting).toBeUndefined()
    expect(seen?.aborted).toBe(true)
    expect(prompted).toEqual({ login: 'person@example.com' })
  })

  it('reports an unavailable autofill UI as unsupported', async () => {
    class NoAutofill {
      static parseCreationOptionsFromJSON = vi.fn()
      static parseRequestOptionsFromJSON = vi.fn()
      static isConditionalMediationAvailable = () => Promise.resolve(false)
    }
    vi.stubGlobal('PublicKeyCredential', NoAutofill)
    const { passkey, fetchMock } = setup(happy)

    await expect(passkey.login({ conditional: true })).rejects.toMatchObject({
      code: 'unsupported',
    })
    expect(fetchMock).not.toHaveBeenCalled()
  })
})

describe('a dismissed prompt', () => {
  it.each(['NotAllowedError', 'AbortError'])(
    'is a quiet cancelled state on enrol for %s',
    async (name) => {
      stubs.create.mockRejectedValueOnce(domError(name))
      const { passkey, fetchMock } = setup(happy)

      await expect(passkey.enrol()).resolves.toBeUndefined()

      expect(passkey.status.value).toBe('cancelled')
      expect(passkey.error.value).toBeUndefined()
      expect(passkey.busy.value).toBe(false)
      // The finish request never went out.
      expect(fetchMock).toHaveBeenCalledTimes(1)
    },
  )

  it.each(['NotAllowedError', 'AbortError'])(
    'is a quiet cancelled state on login for %s',
    async (name) => {
      stubs.get.mockRejectedValueOnce(domError(name))
      const { passkey, fetchMock } = setup(happy)

      await expect(passkey.login()).resolves.toBeUndefined()

      expect(passkey.status.value).toBe('cancelled')
      expect(passkey.error.value).toBeUndefined()
      expect(fetchMock).toHaveBeenCalledTimes(1)
    },
  )

  it('can be tried again and clears the state', async () => {
    stubs.create.mockRejectedValueOnce(domError('NotAllowedError'))
    const { passkey } = setup(happy)

    await passkey.enrol()
    expect(passkey.status.value).toBe('cancelled')
    await expect(passkey.enrol()).resolves.toEqual(PASSKEY)
    expect(passkey.status.value).toBe('done')
  })
})

describe('a refusal of the server', () => {
  it.each([
    ['enrol begin', 'POST /account/passkeys/register/begin', 403, 'reauthentication_required'],
    ['enrol finish', 'POST /account/passkeys/register/finish', 400, 'invalid_passkey'],
    ['enrol finish', 'POST /account/passkeys/register/finish', 400, 'invalid_argument'],
    ['enrol begin', 'POST /account/passkeys/register/begin', 401, 'unauthenticated'],
    ['enrol begin', 'POST /account/passkeys/register/begin', 403, 'permission_denied'],
  ])('maps %s answering %s %i %s', async (_what, route, status, code) => {
    const { passkey } = setup({ ...happy, [route]: () => failure(status, code) })

    const caught = await passkey.enrol().catch((e: unknown) => e)

    expect(caught).toBeInstanceOf(PasskeyError)
    expect(caught).toMatchObject({ code, status, requestId: 'r1' })
    expect(passkey.status.value).toBe('error')
    expect(passkey.error.value).toBe(caught)
  })

  it('maps a refused login to invalid_credentials', async () => {
    const { passkey } = setup({
      ...happy,
      'POST /account/passkeys/login/finish': () => failure(401, 'invalid_credentials'),
    })

    await expect(passkey.login()).rejects.toMatchObject({
      code: 'invalid_credentials',
      status: 401,
    })
  })

  it('maps the last factor and a missing passkey on delete', async () => {
    const lastFactor = setup({
      'POST /account/passkeys/delete': () => failure(409, 'last_factor'),
    })
    await expect(lastFactor.passkey.remove('a')).rejects.toMatchObject({
      code: 'last_factor',
      status: 409,
    })

    const missing = setup({ 'POST /account/passkeys/delete': () => failure(404, 'not_found') })
    await expect(missing.passkey.remove('a')).rejects.toMatchObject({
      code: 'not_found',
      status: 404,
    })
  })

  it.each([
    [429, 'too_many_requests', 'rate_limited'],
    [503, 'unavailable', 'unavailable'],
    [500, 'internal', 'failed'],
    [418, 'something_new', 'failed'],
  ])('maps %i %s to %s', async (status, code, expected) => {
    const { passkey } = setup({
      'POST /account/passkeys/delete': () => failure(status, code),
    })

    await expect(passkey.remove('a')).rejects.toMatchObject({ code: expected })
  })

  it('leaves the busy state after a refusal', async () => {
    const { passkey } = setup({
      ...happy,
      'POST /account/passkeys/register/finish': () => failure(400, 'invalid_passkey'),
    })

    await passkey.enrol().catch(() => undefined)

    expect(passkey.busy.value).toBe(false)
    await expect(passkey.login()).resolves.toBeDefined()
  })
})

describe('a network failure', () => {
  it('is the network code and not a cancelled state', async () => {
    const { passkey, fetchMock } = setup(happy)
    fetchMock.mockRejectedValue(new TypeError(`Failed to fetch ${CHALLENGE}`))

    const caught = await passkey.login().catch((e: unknown) => e)

    expect(caught).toMatchObject({ code: 'network' })
    expect(passkey.status.value).toBe('error')
  })

  it('maps a timeout to network too', async () => {
    const { passkey, fetchMock } = setup(happy, { timeoutMs: 1 })
    fetchMock.mockImplementation(
      (_url, init) =>
        new Promise((_resolve, reject) => {
          init?.signal?.addEventListener('abort', () => reject(domError('TimeoutError')))
        }),
    )

    await expect(passkey.enrol()).rejects.toMatchObject({ code: 'network' })
  })
})

describe('an unsupported browser', () => {
  it('reports supported false and refuses the ceremonies without a request', async () => {
    removeNavigatorCredentials()
    const { passkey, fetchMock } = setup(happy)

    expect(passkey.supported.value).toBe(false)
    await expect(passkey.enrol()).rejects.toMatchObject({ code: 'unsupported' })
    await expect(passkey.login()).rejects.toMatchObject({ code: 'unsupported' })
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('still lists and removes, which need no WebAuthn', async () => {
    removeNavigatorCredentials()
    const { passkey } = setup({
      'GET /account/passkeys': () => answer(200, { passkeys: [PASSKEY] }),
      'POST /account/passkeys/delete': () => answer(204),
    })

    await expect(passkey.list()).resolves.toEqual([PASSKEY])
    await expect(passkey.remove('fake-id')).resolves.toBeUndefined()
  })
})

function gate(): { wait: Promise<void>; release: () => void } {
  let release: () => void = () => undefined
  const wait = new Promise<void>((resolve) => {
    release = resolve
  })
  return { wait, release }
}

describe('one ceremony per page', () => {
  it('refuses an enrolment of a second instance while one runs', async () => {
    const held = gate()
    const { passkey, client, fetchMock } = setup({
      ...happy,
      'POST /account/passkeys/register/begin': async () => {
        await held.wait
        return answer(200, { public_key: { challenge: CHALLENGE } })
      },
    })
    const other = usePasskey({ client })

    const running = passkey.enrol()
    await expect(other.enrol()).rejects.toMatchObject({ code: 'busy' })
    await expect(other.login()).rejects.toMatchObject({ code: 'busy' })
    held.release()
    await running

    // One begin, not three: a second begin would replace the challenge cookie.
    expect(fetchMock.mock.calls.filter(([url]) => (url as string).endsWith('/begin'))).toHaveLength(
      1,
    )
    expect(other.status.value).toBe('idle')
  })

  it('lets a conditional login yield to a modal ceremony without a request', async () => {
    const held = gate()
    const { passkey, client, fetchMock } = setup({
      ...happy,
      'POST /account/passkeys/register/begin': async () => {
        await held.wait
        return answer(200, { public_key: { challenge: CHALLENGE } })
      },
    })
    const other = usePasskey({ client })

    const running = passkey.enrol()
    await expect(other.login({ conditional: true })).resolves.toBeUndefined()
    held.release()
    await running

    expect(fetchMock.mock.calls.map(([url]) => url)).not.toContain(
      '/api/v1/account/passkeys/login/begin',
    )
    expect(stubs.get).not.toHaveBeenCalled()
  })

  it('ends a conditional login that waits for the autofill UI when enrol starts', async () => {
    let seen: AbortSignal | undefined
    stubs.get.mockImplementationOnce(
      (args: { signal: AbortSignal }) =>
        new Promise((_resolve, reject) => {
          seen = args.signal
          args.signal.addEventListener('abort', () => {
            reject(domError('AbortError'))
          })
        }),
    )
    const { passkey, client } = setup(happy)
    const other = usePasskey({ client })

    const waiting = other.login({ conditional: true })
    await vi.waitFor(() => expect(seen).toBeDefined())
    const enrolled = await passkey.enrol()

    expect(seen?.aborted).toBe(true)
    await expect(waiting).resolves.toBeUndefined()
    expect(enrolled).toEqual(PASSKEY)
  })

  it('ends a conditional login that is still in its begin request when enrol starts', async () => {
    let beginSignal: AbortSignal | undefined
    const { passkey, client, fetchMock } = setup({
      ...happy,
      'POST /account/passkeys/login/begin': (_body, init) =>
        new Promise<Response>((_resolve, reject) => {
          beginSignal = init?.signal ?? undefined
          init?.signal?.addEventListener('abort', () => {
            reject(domError('AbortError'))
          })
        }),
    })
    const other = usePasskey({ client })

    const waiting = other.login({ conditional: true })
    await vi.waitFor(() => expect(beginSignal).toBeDefined())
    await passkey.enrol()

    // The abort reached the request and was not taken for a failure.
    await expect(waiting).resolves.toBeUndefined()
    expect(beginSignal?.aborted).toBe(true)
    expect(stubs.get).not.toHaveBeenCalled()
    expect(fetchMock.mock.calls.some(([url]) => (url as string).endsWith('/login/finish'))).toBe(
      false,
    )
  })
})

describe('a passkey with a device token client', () => {
  it('refuses both ceremonies without a request', async () => {
    const fetchMock = vi.fn<typeof globalThis.fetch>()
    const client = createClient({ fetch: fetchMock, auth: { mode: 'bearer' } })
    const passkey = usePasskey({ client })

    await expect(passkey.enrol()).rejects.toMatchObject({ code: 'bearer_unsupported' })
    await expect(passkey.login()).rejects.toMatchObject({ code: 'bearer_unsupported' })
    expect(fetchMock).not.toHaveBeenCalled()
  })
})

describe('a new ceremony', () => {
  it('clears the error of the one before', async () => {
    let refuse = true
    const { passkey } = setup({
      ...happy,
      'POST /account/passkeys/login/finish': () =>
        refuse ? failure(401, 'invalid_credentials') : answer(200, { login: 'person@example.com' }),
    })

    await passkey.login().catch(() => undefined)
    expect(passkey.error.value).toBeDefined()
    refuse = false
    await passkey.login()

    expect(passkey.error.value).toBeUndefined()
    expect(passkey.status.value).toBe('done')
  })

  it('takes a null credential for a cancel', async () => {
    stubs.create.mockResolvedValueOnce(null)
    const { passkey, fetchMock } = setup(happy)

    await expect(passkey.enrol()).resolves.toBeUndefined()

    expect(passkey.status.value).toBe('cancelled')
    expect(passkey.error.value).toBeUndefined()
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })
})

describe('what an operator can tell', () => {
  it('keeps the name of a browser error as the reason, and no message', async () => {
    stubs.create.mockRejectedValueOnce(domError('SecurityError'))
    const { passkey } = setup(happy)

    const caught = (await passkey.enrol().catch((e: unknown) => e)) as PasskeyError

    expect(caught.code).toBe('failed')
    expect(caught.reason).toBe('SecurityError')
    expect(JSON.stringify([caught.message, caught.reason])).not.toContain(CHALLENGE)
  })

  it('reduces a name outside the fixed set to other', async () => {
    stubs.create.mockRejectedValueOnce(domError(`Custom ${CHALLENGE}`))
    const { passkey } = setup(happy)

    const caught = (await passkey.enrol().catch((e: unknown) => e)) as PasskeyError

    expect(caught.reason).toBe('other')
  })

  it('has no reason for a failure that is not the browser', async () => {
    const { passkey } = setup({
      ...happy,
      'POST /account/passkeys/register/finish': () => failure(400, 'invalid_passkey'),
    })

    const caught = (await passkey.enrol().catch((e: unknown) => e)) as PasskeyError

    expect(caught.reason).toBe('')
  })

  it('maps an excluded authenticator to already_registered on enrol only', async () => {
    stubs.get.mockRejectedValueOnce(domError('InvalidStateError'))
    const { passkey } = setup(happy)

    await expect(passkey.login()).rejects.toMatchObject({
      code: 'failed',
      reason: 'InvalidStateError',
    })
  })

  it('takes a plain text 403 of the CSRF middleware for a refused request', async () => {
    const { passkey } = setup({
      'POST /account/passkeys/register/begin': () =>
        new Response('Forbidden', { status: 403, headers: { 'Content-Type': 'text/plain' } }),
    })

    await expect(passkey.enrol()).rejects.toMatchObject({
      code: 'permission_denied',
      status: 403,
    })
  })
})

describe('double submit', () => {
  it('shares one ceremony between two enrol calls', async () => {
    let release: () => void = () => undefined
    const gate = new Promise<void>((resolve) => {
      release = resolve
    })
    const { passkey, fetchMock } = setup({
      ...happy,
      'POST /account/passkeys/register/begin': async () => {
        await gate
        return answer(200, { public_key: { challenge: CHALLENGE } })
      },
    })

    const first = passkey.enrol('A')
    const second = passkey.enrol('B')
    expect(passkey.busy.value).toBe(true)
    release()
    const [a, b] = await Promise.all([first, second])

    expect(a).toBe(b)
    expect(fetchMock).toHaveBeenCalledTimes(2)
    expect(stubs.create).toHaveBeenCalledTimes(1)
    expect(JSON.parse(fetchMock.mock.calls[1][1]?.body as string).name).toBe('A')
  })

  it('refuses a login while an enrolment runs', async () => {
    let release: () => void = () => undefined
    const gate = new Promise<void>((resolve) => {
      release = resolve
    })
    const { passkey } = setup({
      ...happy,
      'POST /account/passkeys/register/begin': async () => {
        await gate
        return answer(200, { public_key: { challenge: CHALLENGE } })
      },
    })

    const running = passkey.enrol()
    await expect(passkey.login()).rejects.toMatchObject({ code: 'busy' })
    release()
    await running
    expect(passkey.status.value).toBe('done')
  })
})

describe('list and remove', () => {
  it('lists with a GET and no body', async () => {
    const { passkey, fetchMock } = setup({
      'GET /account/passkeys': () => answer(200, { passkeys: [PASSKEY] }),
    })

    await expect(passkey.list()).resolves.toEqual([PASSKEY])
    expect(fetchMock.mock.calls[0][1]?.method).toBe('GET')
    expect(fetchMock.mock.calls[0][1]?.body).toBeUndefined()
  })

  it('answers an empty list', async () => {
    const { passkey } = setup({ 'GET /account/passkeys': () => answer(200, { passkeys: [] }) })

    await expect(passkey.list()).resolves.toEqual([])
  })

  it('removes with a POST of the id field', async () => {
    const { passkey, fetchMock } = setup({ 'POST /account/passkeys/delete': () => answer(204) })

    await passkey.remove('fake-id')

    expect(fetchMock.mock.calls[0][0]).toBe('/api/v1/account/passkeys/delete')
    expect(fetchMock.mock.calls[0][1]?.method).toBe('POST')
    expect(JSON.parse(fetchMock.mock.calls[0][1]?.body as string)).toEqual({ id: 'fake-id' })
  })

  it('honours a custom route prefix', async () => {
    const fetchMock = vi.fn<typeof globalThis.fetch>(() =>
      Promise.resolve(answer(200, { passkeys: [] })),
    )
    const client = createClient({ fetch: fetchMock })
    const passkey = usePasskey({ client, path: '/me/keys' })

    await passkey.list()

    expect(fetchMock.mock.calls[0][0]).toBe('/api/v1/me/keys')
  })
})

describe('secrets', () => {
  function dump(error: unknown): string {
    const e = error as PasskeyError
    return JSON.stringify([e.message, e.name, e.code, e.status, e.requestId, e.cause, e.stack])
  }

  it('keeps server text, details and the challenge out of a refusal', async () => {
    const { passkey } = setup({
      ...happy,
      'POST /account/passkeys/register/finish': () => failure(400, 'invalid_passkey'),
    })

    const caught = await passkey.enrol().catch((e: unknown) => e)

    expect(dump(caught)).not.toContain(CHALLENGE)
    expect((caught as PasskeyError).cause).toBeUndefined()
  })

  it('keeps the text of a browser error out of the error', async () => {
    stubs.create.mockRejectedValueOnce(domError('SecurityError'))
    const { passkey } = setup(happy)

    const caught = await passkey.enrol().catch((e: unknown) => e)

    expect(dump(caught)).not.toContain(CHALLENGE)
    expect((caught as PasskeyError).cause).toBeUndefined()
  })

  it('keeps the text of a parse error out of the error', async () => {
    stubs.parseRequest.mockImplementationOnce(() => {
      throw new TypeError(`bad options ${CHALLENGE}`)
    })
    const { passkey } = setup(happy)

    const caught = await passkey.login().catch((e: unknown) => e)

    expect(caught).toMatchObject({ code: 'failed' })
    expect(dump(caught)).not.toContain(CHALLENGE)
    expect((caught as PasskeyError).cause).toBeUndefined()
  })

  it('keeps the text of a transport error out of the error', async () => {
    const { passkey, fetchMock } = setup(happy)
    fetchMock.mockRejectedValue(new TypeError(`Failed to fetch ${CHALLENGE}`))

    const caught = await passkey.login().catch((e: unknown) => e)

    expect(dump(caught)).not.toContain(CHALLENGE)
    expect((caught as PasskeyError).cause).toBeUndefined()
  })

  it('keeps the credential out of an error after finish was refused', async () => {
    const { passkey } = setup({
      ...happy,
      'POST /account/passkeys/login/finish': () => failure(401, 'invalid_credentials'),
    })

    const caught = await passkey.login().catch((e: unknown) => e)

    expect(dump(caught)).not.toContain(CREDENTIAL_ID)
    expect(dump(caught)).not.toContain(ATTESTATION)
  })
})
