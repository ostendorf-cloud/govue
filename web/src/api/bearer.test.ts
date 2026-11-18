// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { BearerAuthOptions, BearerStorageAuthOptions } from './client'
import { createClient } from './client'
import { ErrorCode, isApiError } from './errors'
import type { TokenStorage } from './tokenStorage'

// Fake token values only. The two long, distinctive strings make a leak easy
// to find with a substring search.
const OLD = 'govue-test-token-OLD-0000000000000000'
const NEW = 'govue-test-token-NEW-1111111111111111'
const SIGNED = 'govue-test-token-SIGNED-222222222222'

const HOUR = 3_600_000
const START = new Date('2026-03-01T10:00:00Z')

function answer(status: number, body?: unknown): Response {
  const init: ResponseInit = { status, headers: { 'Content-Type': 'application/json' } }
  return body === undefined ? new Response(null, init) : new Response(JSON.stringify(body), init)
}

function errorBody(code: string, message: string) {
  return { error: { code, message } }
}

function issued(token: string) {
  return { token, token_id: 'dev-1', expires_at: '2026-09-01T00:00:00Z' }
}

/** aged is an issue time that makes a token due on the first use after start. */
function aged(): number {
  return Date.now() - 2 * HOUR
}

/** record is the value the client keeps in the storage. */
function record(token: string, at = Date.now()): string {
  return JSON.stringify({ v: 1, token, at })
}

function storedToken(value: string | undefined): string | undefined {
  return value === undefined ? undefined : (JSON.parse(value) as { token: string }).token
}

interface Call {
  url: string
  method: string
  init: RequestInit
  headers: Record<string, string>
  body: unknown
}

interface Deferred<T> {
  promise: Promise<T>
  resolve: (value: T) => void
}

function deferred<T>(): Deferred<T> {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((r) => {
    resolve = r
  })
  return { promise, resolve }
}

interface FakeStorage extends TokenStorage {
  value: string | undefined
  sets: string[]
  clears: number
  failGet?: Error
  failSet?: Error
  failClear?: Error
  setGate?: Promise<void>
}

function fakeStorage(initial?: string): FakeStorage {
  const storage: FakeStorage = {
    value: initial,
    sets: [],
    clears: 0,
    get() {
      if (storage.failGet) return Promise.reject(storage.failGet)
      return Promise.resolve(storage.value ?? null)
    },
    async set(value) {
      if (storage.failSet) throw storage.failSet
      storage.sets.push(value)
      if (storage.setGate) await storage.setGate
      storage.value = value
    },
    clear() {
      if (storage.failClear) return Promise.reject(storage.failClear)
      storage.clears++
      storage.value = undefined
      return Promise.resolve()
    },
  }
  return storage
}

type Handler = (call: Call) => Response | Promise<Response>

interface Server {
  /** Answers the rotate route. The default is the quiet refusal of a young token. */
  rotate: Handler
  /** Answers POST /auth/token. */
  issue: Handler
  /** Answers DELETE /auth/token. */
  signOut: Handler
  /** Answers every other path. */
  api: Handler
}

function setup(
  options: {
    initial?: string
    auth?: Partial<Pick<BearerStorageAuthOptions, 'rotateAfterMs' | 'deviceName' | 'rotation'>>
    onUnauthenticated?: () => void
  } = {},
) {
  const calls: Call[] = []
  const storage = fakeStorage(options.initial)
  const server: Server = {
    rotate: () => answer(409, errorBody('rotation_refused', 'refused')),
    issue: () => answer(200, issued(SIGNED)),
    signOut: () => answer(204),
    api: () => answer(200, { ok: true }),
  }
  const fetchFake = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
    const method = init?.method ?? 'GET'
    const call: Call = {
      url,
      method,
      init: init ?? {},
      headers: (init?.headers ?? {}) as Record<string, string>,
      body: typeof init?.body === 'string' ? JSON.parse(init.body) : undefined,
    }
    calls.push(call)
    if (url.endsWith('/auth/token/rotate')) return server.rotate(call)
    if (url.endsWith('/auth/token') && method === 'POST') return server.issue(call)
    if (url.endsWith('/auth/token') && method === 'DELETE') return server.signOut(call)
    return server.api(call)
  }) as unknown as typeof globalThis.fetch

  const client = createClient({
    fetch: fetchFake,
    onUnauthenticated: options.onUnauthenticated,
    auth: { mode: 'bearer', storage, rotation: 'owner', ...options.auth },
  })
  const rotations = () => calls.filter((c) => c.url.endsWith('/auth/token/rotate'))
  const requests = () =>
    calls.filter((c) => !c.url.endsWith('/auth/token/rotate') && !c.url.endsWith('/auth/token'))
  return { client, storage, server, calls, rotations, requests }
}

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(START)
})

afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
})

describe('bearer mode requests', () => {
  it('sends the exact init: bearer header, omit credentials, no CSRF header', async () => {
    const t = setup({ initial: record(OLD) })

    await t.client.get('/things')

    expect(t.requests()[0].init).toStrictEqual({
      method: 'GET',
      headers: { Accept: 'application/json', Authorization: `Bearer ${OLD}` },
      credentials: 'omit',
      body: undefined,
      signal: expect.any(AbortSignal),
    })
  })

  it('sends no CSRF header on an unsafe method', async () => {
    const t = setup({ initial: record(OLD) })

    await t.client.post('/things', { title: 'lamp' })
    await t.client.del('/things/7')

    for (const call of t.requests()) {
      expect(call.init.credentials).toBe('omit')
      expect(Object.keys(call.headers).map((k) => k.toLowerCase())).not.toContain('x-csrf-token')
      expect(call.headers.Authorization).toBe(`Bearer ${OLD}`)
    }
  })

  it('refuses a csrfToken or credentials option in bearer mode', () => {
    const base = { fetch: vi.fn() as unknown as typeof fetch }
    expect(() => createClient({ ...base, csrfToken: () => 'x', auth: { mode: 'bearer' } })).toThrow(
      TypeError,
    )
    expect(() =>
      createClient({ ...base, credentials: 'include', auth: { mode: 'bearer' } }),
    ).toThrow(TypeError)
  })

  it('owns the Authorization and CSRF headers and drops the caller copies', async () => {
    const t = setup({ initial: record(OLD) })

    await t.client.post(
      '/things',
      {},
      {
        headers: { authorization: 'Bearer caller', 'X-CSRF-Token': 'csrf', 'X-Other': 'kept' },
      },
    )

    const headers = t.requests()[0].headers
    expect(headers.Authorization).toBe(`Bearer ${OLD}`)
    expect(headers.authorization).toBeUndefined()
    expect(headers['X-CSRF-Token']).toBeUndefined()
    expect(headers['X-Other']).toBe('kept')
  })

  it('sends no Authorization header without a token and does not rotate', async () => {
    const t = setup()

    await t.client.get('/things')

    expect(t.rotations()).toHaveLength(0)
    expect(t.requests()[0].headers.Authorization).toBeUndefined()
    expect(t.requests()[0].init.credentials).toBe('omit')
  })

  it('never reads document.cookie', async () => {
    const read = vi.spyOn(document, 'cookie', 'get')
    const t = setup({ initial: record(OLD) })

    await t.client.get('/things')
    await t.client.signIn({ login: 'a', password: 'b' })
    await t.client.signOut()

    expect(read).not.toHaveBeenCalled()
  })

  it('keeps the token out of every URL and out of every header but Authorization', async () => {
    const t = setup({ initial: record(OLD) })
    t.server.rotate = () => answer(200, issued(NEW))

    await t.client.get('/things', { query: { q: 'lamp' } })
    await t.client.post('/things', { a: 1 })

    for (const call of t.calls) {
      expect(call.url).not.toContain(NEW)
      expect(call.url).not.toContain(OLD)
      for (const [name, value] of Object.entries(call.headers)) {
        if (name !== 'Authorization') {
          expect(`${name}: ${value}`).not.toMatch(/govue-test-token/)
        }
      }
      expect(JSON.stringify(call.body ?? null)).not.toMatch(/govue-test-token/)
    }
  })
})

describe('cookie mode stays as it was', () => {
  it('sends the exact init of the cookie client, without Authorization', async () => {
    const calls: Array<{ url: string; init: RequestInit }> = []
    const fetchFake = ((input: string, init?: RequestInit) => {
      calls.push({ url: input, init: init ?? {} })
      return Promise.resolve(answer(200, { ok: true }))
    }) as unknown as typeof globalThis.fetch

    for (const auth of [undefined, { mode: 'cookie' as const }]) {
      calls.length = 0
      const client = createClient({ fetch: fetchFake, csrfToken: () => 'csrf-1', auth })
      await client.get('/things')
      await client.post('/things', { a: 1 })

      expect(calls[0].init).toStrictEqual({
        method: 'GET',
        headers: { Accept: 'application/json' },
        credentials: 'same-origin',
        body: undefined,
        signal: expect.any(AbortSignal),
      })
      expect(calls[1].init).toStrictEqual({
        method: 'POST',
        headers: {
          Accept: 'application/json',
          'Content-Type': 'application/json',
          'X-CSRF-Token': 'csrf-1',
        },
        credentials: 'same-origin',
        body: '{"a":1}',
        signal: expect.any(AbortSignal),
      })
    }
  })

  it('calls onUnauthenticated on every 401 as before', async () => {
    const hook = vi.fn()
    const fetchFake = (() =>
      Promise.resolve(answer(401, errorBody('unauthenticated', 'no')))) as typeof fetch
    const client = createClient({ fetch: fetchFake, onUnauthenticated: hook })

    await expect(client.get('/a')).rejects.toBeDefined()
    await expect(client.get('/b')).rejects.toBeDefined()

    expect(hook).toHaveBeenCalledTimes(2)
  })

  it('has no signIn or signOut', () => {
    const client = createClient({ fetch: vi.fn() as unknown as typeof fetch })

    expect('signIn' in client).toBe(false)
    expect('signOut' in client).toBe(false)
  })
})

describe('rotation', () => {
  it('rotates on the first use after start, writes before it uses the token', async () => {
    const t = setup({ initial: record(OLD, aged()) })
    t.server.rotate = () => answer(200, issued(NEW))

    await t.client.get('/things')
    await t.client.get('/things')

    expect(t.rotations()).toHaveLength(1)
    expect(t.rotations()[0].method).toBe('POST')
    expect(t.rotations()[0].url).toBe('/api/v1/auth/token/rotate')
    expect(t.rotations()[0].headers.Authorization).toBe(`Bearer ${OLD}`)
    expect(t.rotations()[0].init.credentials).toBe('omit')
    expect(Object.keys(t.rotations()[0].body as object)).toEqual(['nonce'])
    expect(storedToken(t.storage.value)).toBe(NEW)
    // The stale token is never used again after the rotation.
    expect(t.requests().map((c) => c.headers.Authorization)).toEqual([
      `Bearer ${NEW}`,
      `Bearer ${NEW}`,
    ])
  })

  it('sends a valid random nonce, a new one per attempt', async () => {
    const t = setup({ initial: record(OLD, aged()), auth: { rotateAfterMs: HOUR } })
    t.server.rotate = () => answer(200, issued(NEW))

    await t.client.get('/a')
    vi.setSystemTime(Date.now() + 2 * HOUR)
    await t.client.get('/b')

    const nonces = t.rotations().map((c) => (c.body as { nonce: string }).nonce)
    expect(nonces).toHaveLength(2)
    for (const nonce of nonces) {
      expect(nonce).toMatch(/^[!-~]{16,128}$/)
    }
    expect(nonces[0]).not.toBe(nonces[1])
  })

  it('honours the interval by the stored timestamp', async () => {
    const t = setup({ initial: record(OLD, aged()) })
    let n = 0
    t.server.rotate = () => answer(200, issued(`${NEW}-${++n}`))

    await t.client.get('/start')
    expect(t.rotations()).toHaveLength(1)

    vi.setSystemTime(Date.now() + 23 * HOUR + 59 * 60_000)
    await t.client.get('/a')
    expect(t.rotations()).toHaveLength(1)

    vi.setSystemTime(Date.now() + 2 * 60_000)
    await t.client.get('/b')
    expect(t.rotations()).toHaveLength(2)
    expect(storedToken(t.storage.value)).toBe(`${NEW}-2`)
  })

  it('takes its own interval and rejects a bad one', async () => {
    const t = setup({ initial: record(OLD, aged()), auth: { rotateAfterMs: HOUR } })
    t.server.rotate = () => answer(200, issued(NEW))

    await t.client.get('/start')
    vi.setSystemTime(Date.now() + HOUR + 1)
    await t.client.get('/a')

    expect(t.rotations()).toHaveLength(2)
    for (const bad of [0, -1, Number.NaN, Number.POSITIVE_INFINITY]) {
      expect(() =>
        createClient({ auth: { mode: 'bearer', rotateAfterMs: bad }, fetch: vi.fn() as never }),
      ).toThrow(RangeError)
    }
  })

  it('shares one rotation between 20 concurrent requests', async () => {
    const t = setup({ initial: record(OLD, aged()) })
    const gate = deferred<Response>()
    t.server.rotate = () => gate.promise

    const all = Array.from({ length: 20 }, (_, i) => t.client.get(`/things/${i}`))
    await vi.waitFor(() => expect(t.rotations()).toHaveLength(1))
    gate.resolve(answer(200, issued(NEW)))
    await Promise.all(all)

    expect(t.rotations()).toHaveLength(1)
    expect(t.storage.sets).toHaveLength(1)
    expect(t.requests()).toHaveLength(20)
    for (const call of t.requests()) {
      expect(call.headers.Authorization).toBe(`Bearer ${NEW}`)
    }
  })

  it('uses the new token only after the storage write finished', async () => {
    const t = setup({ initial: record(OLD, aged()) })
    const gate = deferred<void>()
    t.storage.setGate = gate.promise
    t.server.rotate = () => answer(200, issued(NEW))

    const pending = t.client.get('/things')
    await vi.waitFor(() => expect(t.storage.sets).toHaveLength(1))
    await new Promise((r) => setTimeout(r, 10))
    expect(t.requests()).toHaveLength(0)

    gate.resolve()
    await pending

    expect(t.requests()).toHaveLength(1)
    expect(t.requests()[0].headers.Authorization).toBe(`Bearer ${NEW}`)
    expect(storedToken(t.storage.value)).toBe(NEW)
  })

  it('keeps the old token on a network error and retries with the same nonce', async () => {
    const t = setup({ initial: record(OLD, aged()) })
    t.server.rotate = () => {
      throw new TypeError('fetch failed')
    }

    await t.client.get('/a')

    expect(t.requests()[0].headers.Authorization).toBe(`Bearer ${OLD}`)
    expect(t.storage.sets).toHaveLength(0)
    expect(storedToken(t.storage.value)).toBe(OLD)

    t.server.rotate = () => answer(200, issued(NEW))
    await t.client.get('/b')

    expect(t.rotations()).toHaveLength(2)
    expect((t.rotations()[1].body as { nonce: string }).nonce).toBe(
      (t.rotations()[0].body as { nonce: string }).nonce,
    )
    expect(t.requests()[1].headers.Authorization).toBe(`Bearer ${NEW}`)
  })

  it('keeps the token on a server error of the rotation', async () => {
    const t = setup({ initial: record(OLD) })
    t.server.rotate = () => answer(503, errorBody('unavailable', 'later'))
    const hook = vi.fn()
    const u = setup({ initial: record(OLD), onUnauthenticated: hook })
    u.server.rotate = () => answer(503, errorBody('unavailable', 'later'))

    await u.client.get('/a')

    expect(u.requests()[0].headers.Authorization).toBe(`Bearer ${OLD}`)
    expect(u.storage.clears).toBe(0)
    expect(hook).not.toHaveBeenCalled()
  })

  it('treats 409 rotation_refused as quiet: token kept, no sign out, no retry storm', async () => {
    const hook = vi.fn()
    const t = setup({ initial: record(OLD, START.getTime() - 25 * HOUR), onUnauthenticated: hook })

    await t.client.get('/a')
    await t.client.get('/b')
    expect(t.rotations()).toHaveLength(1)
    expect(t.requests().map((c) => c.headers.Authorization)).toEqual([
      `Bearer ${OLD}`,
      `Bearer ${OLD}`,
    ])
    expect(t.storage.clears).toBe(0)
    expect(storedToken(t.storage.value)).toBe(OLD)
    expect(hook).not.toHaveBeenCalled()

    // After the backoff the stale token is due again.
    vi.setSystemTime(Date.now() + 6 * 60_000)
    await t.client.get('/c')
    expect(t.rotations()).toHaveLength(2)
  })

  it('clears the storage and calls onUnauthenticated once on a 401 rotation', async () => {
    const hook = vi.fn()
    const t = setup({ initial: record(OLD, aged()), onUnauthenticated: hook })
    const gate = deferred<Response>()
    t.server.rotate = () => gate.promise

    const all = Array.from({ length: 20 }, (_, i) =>
      t.client.get(`/things/${i}`).then(
        () => 'ok',
        (error: unknown) => error,
      ),
    )
    await vi.waitFor(() => expect(t.rotations()).toHaveLength(1))
    gate.resolve(answer(401, errorBody('unauthenticated', 'no')))
    const results = await Promise.all(all)

    expect(hook).toHaveBeenCalledTimes(1)
    expect(t.storage.value).toBeUndefined()
    expect(t.requests()).toHaveLength(0)
    for (const result of results) {
      expect(isApiError(result)).toBe(true)
      expect((result as { status: number }).status).toBe(401)
    }
  })

  it('does not rotate when there is no token, not even after a sign out', async () => {
    const t = setup()

    await t.client.get('/a')
    await t.client.signOut()
    await t.client.get('/b')

    expect(t.rotations()).toHaveLength(0)
  })

  it('does not rotate right after a sign in', async () => {
    const t = setup()

    await t.client.signIn({ login: 'a', password: 'b' })
    await t.client.get('/a')

    expect(t.rotations()).toHaveLength(0)
    expect(t.requests()[0].headers.Authorization).toBe(`Bearer ${SIGNED}`)
  })

  it('drops a rotation result that a sign out overtook', async () => {
    const t = setup({ initial: record(OLD, aged()) })
    const gate = deferred<Response>()
    t.server.rotate = () => gate.promise

    const pending = t.client.get('/a')
    await vi.waitFor(() => expect(t.rotations()).toHaveLength(1))
    await t.client.signOut()
    gate.resolve(answer(200, issued(NEW)))
    await pending

    expect(t.storage.value).toBeUndefined()
    expect(t.storage.sets).toHaveLength(0)
    const out = t.calls.find((c) => c.method === 'DELETE')
    expect(out?.headers.Authorization).toBe(`Bearer ${OLD}`)
    expect(t.requests()[0].headers.Authorization).toBeUndefined()
  })

  it('drops a rotation result that a sign in overtook', async () => {
    const t = setup({ initial: record(OLD, aged()) })
    const gate = deferred<Response>()
    t.server.rotate = () => gate.promise

    const pending = t.client.get('/a')
    await vi.waitFor(() => expect(t.rotations()).toHaveLength(1))
    await t.client.signIn({ login: 'a', password: 'b' })
    gate.resolve(answer(200, issued(NEW)))
    await pending

    expect(storedToken(t.storage.value)).toBe(SIGNED)
    expect(t.requests()[0].headers.Authorization).toBe(`Bearer ${SIGNED}`)
  })
})

describe('a 401 on an ordinary request', () => {
  it('clears the storage and calls the hook once for concurrent requests', async () => {
    const hook = vi.fn()
    const t = setup({ initial: record(OLD), onUnauthenticated: hook })
    t.server.api = () => answer(401, errorBody('unauthenticated', 'no'))

    const results = await Promise.allSettled(
      Array.from({ length: 5 }, (_, i) => t.client.get(`/things/${i}`)),
    )

    expect(results.every((r) => r.status === 'rejected')).toBe(true)
    expect(hook).toHaveBeenCalledTimes(1)
    expect(t.storage.value).toBeUndefined()
    expect(t.storage.clears).toBe(1)
  })

  it('does not clear a newer token after a late 401 of an old one', async () => {
    const hook = vi.fn()
    const t = setup({ initial: record(OLD), onUnauthenticated: hook })
    t.server.rotate = () => answer(200, issued(NEW))
    const gate = deferred<Response>()
    t.server.api = () => gate.promise

    const late = t.client.get('/late').catch(() => undefined)
    await vi.waitFor(() => expect(t.requests()).toHaveLength(1))
    // A sign in replaces the token while the first request is out.
    await t.client.signIn({ login: 'a', password: 'b' })
    gate.resolve(answer(401, errorBody('unauthenticated', 'no')))
    await late

    expect(storedToken(t.storage.value)).toBe(SIGNED)
    expect(hook).not.toHaveBeenCalled()
  })

  it('keeps the token when the request opts out of the hook', async () => {
    const hook = vi.fn()
    const t = setup({ initial: record(OLD), onUnauthenticated: hook })
    t.server.api = () => answer(401, errorBody('invalid_credentials', 'no'))

    await expect(t.client.post('/x', {}, { skipUnauthenticatedHook: true })).rejects.toBeDefined()

    expect(hook).not.toHaveBeenCalled()
    expect(storedToken(t.storage.value)).toBe(OLD)
  })

  it('calls the hook for a 401 without a token, like the cookie mode', async () => {
    const hook = vi.fn()
    const t = setup({ onUnauthenticated: hook })
    t.server.api = () => answer(401, errorBody('unauthenticated', 'no'))

    await expect(t.client.get('/x')).rejects.toBeDefined()

    expect(hook).toHaveBeenCalledTimes(1)
  })
})

describe('the token never leaks', () => {
  it('keeps the token out of a thrown transport error', async () => {
    const t = setup({ initial: record(OLD) })
    t.server.api = () => {
      throw new Error(`request failed: Authorization: Bearer ${OLD} to /api/v1/things?t=${OLD}`)
    }

    const error = await t.client.get('/things').catch((e: unknown) => e)

    expect(isApiError(error)).toBe(true)
    const e = error as Error & { code: string; cause?: unknown }
    expect(e.code).toBe(ErrorCode.network)
    expect(e.cause).toBeUndefined()
    expect(JSON.stringify([e.message, e.name, e.stack, e.code, Object.keys(e)])).not.toContain(OLD)
    expect(JSON.stringify(e)).not.toContain(OLD)
  })

  it('keeps the token out of a rotation error and a timeout', async () => {
    const t = setup({ initial: record(OLD) })
    t.server.rotate = () => {
      throw new Error(`boom ${OLD}`)
    }
    t.server.api = () => {
      throw new DOMException(`aborted ${OLD}`, 'AbortError')
    }

    const error = await t.client.get('/things').catch((e: unknown) => e)

    expect(JSON.stringify(error)).not.toContain(OLD)
    expect((error as Error).message).not.toContain(OLD)
    expect((error as { cause?: unknown }).cause).toBeUndefined()
  })

  it('writes nothing to the console that holds a token', async () => {
    const spies = (['log', 'info', 'warn', 'error', 'debug', 'trace'] as const).map((m) =>
      vi.spyOn(console, m).mockImplementation(() => undefined),
    )
    const t = setup({ initial: record(OLD) })
    t.server.rotate = () => answer(200, issued(NEW))

    await t.client.get('/ok')
    t.server.api = () => {
      throw new Error(`fail ${NEW}`)
    }
    await t.client.get('/fail').catch(() => undefined)
    t.server.api = () => answer(401, errorBody('unauthenticated', 'no'))
    await t.client.get('/401').catch(() => undefined)
    const u = setup({ initial: record(OLD) })
    u.storage.failGet = new Error(`keychain ${OLD}`)
    await u.client.get('/x').catch(() => undefined)
    await t.client.signIn({ login: 'a', password: 'b' })
    await t.client.signOut().catch(() => undefined)

    for (const spy of spies) {
      expect(JSON.stringify(spy.mock.calls)).not.toMatch(/govue-test-token/)
    }
  })
})

describe('a failing storage fails closed', () => {
  it('throws a typed error on a failing read and sends nothing', async () => {
    const t = setup({ initial: record(OLD) })
    t.storage.failGet = new Error(`keychain locked ${OLD}`)

    const error = await t.client.get('/things').catch((e: unknown) => e)

    expect(isApiError(error)).toBe(true)
    expect((error as { code: string }).code).toBe(ErrorCode.tokenStorage)
    expect(t.calls).toHaveLength(0)
    expect(JSON.stringify(error)).not.toContain(OLD)
    expect((error as Error).message).not.toContain('keychain')
    expect((error as { cause?: unknown }).cause).toBeUndefined()
  })

  it('throws a typed error when the new token cannot be written and keeps the old one', async () => {
    const t = setup({ initial: record(OLD, aged()) })
    t.server.rotate = () => answer(200, issued(NEW))
    t.storage.failSet = new Error(`disk full ${NEW}`)

    const error = await t.client.get('/things').catch((e: unknown) => e)

    expect((error as { code: string }).code).toBe(ErrorCode.tokenStorage)
    expect(t.requests()).toHaveLength(0)
    expect(storedToken(t.storage.value)).toBe(OLD)
    expect(JSON.stringify(error)).not.toContain(NEW)
  })

  it('throws a typed error on a stored value it cannot read', async () => {
    for (const bad of ['not json', '{"v":2,"token":"x","at":1}', '{"v":1,"token":"","at":1}', '']) {
      const t = setup({ initial: bad })

      const error = await t.client.get('/things').catch((e: unknown) => e)

      expect((error as { code: string }).code).toBe(ErrorCode.tokenStorage)
      expect(t.calls).toHaveLength(0)
    }
  })

  it('throws a typed error when a sign in cannot store the token', async () => {
    const t = setup()
    t.storage.failSet = new Error('no')

    const error = await t.client.signIn({ login: 'a', password: 'b' }).catch((e: unknown) => e)

    expect((error as { code: string }).code).toBe(ErrorCode.tokenStorage)
    expect(JSON.stringify(error)).not.toContain(SIGNED)
  })

  it('throws a typed error when the clear after a 401 fails', async () => {
    const hook = vi.fn()
    const t = setup({ initial: record(OLD), onUnauthenticated: hook })
    t.server.api = () => answer(401, errorBody('unauthenticated', 'no'))
    t.storage.failClear = new Error('no')

    const error = await t.client.get('/x').catch((e: unknown) => e)

    expect((error as { code: string }).code).toBe(ErrorCode.tokenStorage)
    expect(hook).toHaveBeenCalledTimes(1)
  })
})

describe('signIn', () => {
  it('posts the credentials, stores the token and returns the device without it', async () => {
    const t = setup({ initial: record(OLD), auth: { deviceName: 'Pixel 9' } })

    const device = await t.client.signIn({ login: 'ada', password: 'pw', code: '123456' })

    const call = t.calls[0]
    expect(call.method).toBe('POST')
    expect(call.url).toBe('/api/v1/auth/token')
    expect(call.body).toStrictEqual({
      login: 'ada',
      password: 'pw',
      code: '123456',
      device_name: 'Pixel 9',
    })
    expect(call.init.credentials).toBe('omit')
    expect(call.headers.Authorization).toBeUndefined()
    expect(call.headers['X-CSRF-Token']).toBeUndefined()
    expect(storedToken(t.storage.value)).toBe(SIGNED)
    expect(device).toStrictEqual({ deviceId: 'dev-1', expiresAt: '2026-09-01T00:00:00Z' })
    expect(JSON.stringify(device)).not.toContain(SIGNED)
  })

  it('lets the call override the device name and leaves empty fields out', async () => {
    const t = setup({ auth: { deviceName: 'Default' } })

    await t.client.signIn({ login: 'ada', password: 'pw' }, { deviceName: 'Other' })

    expect(t.calls[0].body).toStrictEqual({ login: 'ada', password: 'pw', device_name: 'Other' })
  })

  it('fails with the server answer, keeps the storage and does not call the hook', async () => {
    const hook = vi.fn()
    const t = setup({ initial: record(OLD), onUnauthenticated: hook })
    t.server.issue = () => answer(401, errorBody('invalid_credentials', 'wrong'))

    const error = await t.client.signIn({ login: 'a', password: 'b' }).catch((e: unknown) => e)

    expect((error as { status: number }).status).toBe(401)
    expect(hook).not.toHaveBeenCalled()
    expect(storedToken(t.storage.value)).toBe(OLD)
  })

  it('refuses an answer without a token and stores nothing', async () => {
    const t = setup()
    t.server.issue = () => answer(200, { token_id: 'dev-1' })

    const error = await t.client.signIn({ login: 'a', password: 'b' }).catch((e: unknown) => e)

    expect(isApiError(error)).toBe(true)
    expect(t.storage.sets).toHaveLength(0)
  })

  it('checks the device name before it sends anything', async () => {
    const t = setup()
    const bad = ['x'.repeat(65), 'tab\there', 'zero​width', 'lone\uD800', 'é'.repeat(33)]

    for (const deviceName of bad) {
      const error = await t.client
        .signIn({ login: 'a', password: 'b' }, { deviceName })
        .catch((e: unknown) => e)
      expect((error as { code: string }).code).toBe(ErrorCode.invalidArgument)
    }
    await t.client.signIn({ login: 'a', password: 'b' }, { deviceName: 'x'.repeat(64) })

    expect(t.calls).toHaveLength(1)
    expect(() =>
      createClient({
        auth: { mode: 'bearer', deviceName: 'x'.repeat(65) },
        fetch: vi.fn() as never,
      }),
    ).toThrow(RangeError)
  })

  it('uses the memory storage by default and does not share it between clients', async () => {
    const calls: Call[] = []
    const fetchFake = ((input: string, init?: RequestInit) => {
      const url = input
      calls.push({
        url,
        method: init?.method ?? 'GET',
        init: init ?? {},
        headers: (init?.headers ?? {}) as Record<string, string>,
        body: undefined,
      })
      return Promise.resolve(
        url.endsWith('/auth/token') ? answer(200, issued(SIGNED)) : answer(200, {}),
      )
    }) as unknown as typeof fetch
    const a = createClient({ fetch: fetchFake, auth: { mode: 'bearer' } })
    const b = createClient({ fetch: fetchFake, auth: { mode: 'bearer' } })

    await a.signIn({ login: 'a', password: 'b' })
    await a.get('/x')
    await b.get('/y')

    expect(calls[1].headers.Authorization).toBe(`Bearer ${SIGNED}`)
    expect(calls[2].headers.Authorization).toBeUndefined()
  })
})

describe('the rotation option with a custom storage', () => {
  // A cast, as a JavaScript caller or a loose type would reach createClient.
  const build = (auth: unknown) => createClient({ fetch: vi.fn() as never, auth: auth as never })

  it('throws a TypeError that names the option and both values when rotation is missing', () => {
    const storage = fakeStorage(record(OLD))

    const act = () => build({ mode: 'bearer', storage })

    expect(act).toThrow(TypeError)
    expect(act).toThrow(/auth\.rotation/)
    expect(act).toThrow(/'owner'/)
    expect(act).toThrow(/'none'/)
  })

  it.each([
    ['an invalid string', 'both'],
    ['undefined given explicitly', undefined],
    ['null', null],
    ['an empty string', ''],
    ['a wrong case', 'Owner'],
    ['a boolean', true],
  ])('refuses %s as the rotation of a custom storage', (_name, rotation) => {
    const storage = fakeStorage(record(OLD))

    expect(() => build({ mode: 'bearer', storage, rotation })).toThrow(TypeError)
  })

  it('touches the storage and the network not at all when it refuses', () => {
    const storage = fakeStorage(record(OLD))
    const get = vi.spyOn(storage, 'get')
    const set = vi.spyOn(storage, 'set')
    const clear = vi.spyOn(storage, 'clear')
    const fetchFake = vi.fn()

    expect(() =>
      createClient({ fetch: fetchFake as never, auth: { mode: 'bearer', storage } as never }),
    ).toThrow(TypeError)

    expect(get).not.toHaveBeenCalled()
    expect(set).not.toHaveBeenCalled()
    expect(clear).not.toHaveBeenCalled()
    expect(fetchFake).not.toHaveBeenCalled()
  })

  it('carries no token or storage content in the message', () => {
    const storage = fakeStorage(record(OLD))
    let message = ''
    try {
      build({ mode: 'bearer', storage, rotation: 'both' })
    } catch (error) {
      message = String(error)
    }

    expect(message).not.toBe('')
    expect(message).not.toContain(OLD)
    expect(message).not.toContain('govue-test-token')
    expect(message).not.toContain('both')
  })

  it.each(['owner', 'none'] as const)(
    'works with a custom storage and rotation %s',
    async (rotation) => {
      const storage = fakeStorage(record(OLD))
      const client = createClient({
        fetch: vi.fn(() => Promise.resolve(answer(200, { ok: true }))),
        auth: { mode: 'bearer', storage, rotation },
      })

      await expect(client.get('/x')).resolves.toStrictEqual({ ok: true })
    },
  )

  it('keeps the memory default: no storage, rotation omitted, owner', async () => {
    const calls: string[] = []
    const client = createClient({
      fetch: ((input: string) => {
        calls.push(input)
        return Promise.resolve(
          input.endsWith('/auth/token') ? answer(200, issued(SIGNED)) : answer(200, {}),
        )
      }) as never,
      auth: { mode: 'bearer' },
    })

    await client.signIn({ login: 'a', password: 'b' })

    expect(calls).toHaveLength(1)
  })

  it('treats a null storage as the memory default', async () => {
    const calls: string[] = []
    const client = createClient({
      fetch: ((input: string) => {
        calls.push(input)
        return Promise.resolve(
          input.endsWith('/auth/token') ? answer(200, issued(SIGNED)) : answer(200, {}),
        )
      }) as never,
      auth: { mode: 'bearer', storage: null } as never,
    })

    await client.signIn({ login: 'a', password: 'b' })

    expect(calls).toHaveLength(1)
    expect(() => build({ mode: 'bearer', storage: null, rotation: 'owner' })).not.toThrow()
  })

  it('refuses rotation none with a null storage', () => {
    expect(() => build({ mode: 'bearer', storage: null, rotation: 'none' })).toThrow(TypeError)
  })

  it('reads storage and rotation once and builds what it checked: a rotation getter', async () => {
    const reads = { storage: 0, rotation: 0 }
    const storage = fakeStorage(record(OLD))
    const fetchFake = vi.fn()
    const auth = {
      mode: 'bearer',
      get storage() {
        reads.storage++
        return storage
      },
      get rotation() {
        // 'none' passes the check, a second read would build an owner.
        return ++reads.rotation === 1 ? 'none' : 'owner'
      },
    }

    const client = createClient({ fetch: fetchFake as never, auth: auth as never })
    const error = await client.signIn({ login: 'a', password: 'b' }).catch((e) => e)

    expect(reads).toStrictEqual({ storage: 1, rotation: 1 })
    expect(isApiError(error)).toBe(true)
    expect(fetchFake).not.toHaveBeenCalled()
  })

  it('reads storage and rotation once and builds what it checked: a storage getter', async () => {
    const reads = { storage: 0, rotation: 0 }
    const custom = fakeStorage(record(OLD))
    const fetchFake = vi.fn(() => Promise.resolve(answer(200, {})))
    const auth = {
      mode: 'bearer',
      get storage() {
        // No storage passes the no storage check, a second read would build an owner on custom.
        return ++reads.storage === 1 ? undefined : custom
      },
      get rotation() {
        reads.rotation++
        return undefined
      },
    }

    const client = createClient({ fetch: fetchFake, auth: auth as never })
    await client.get('/x')

    expect(reads).toStrictEqual({ storage: 1, rotation: 1 })
    expect(custom.value).toBe(record(OLD))
    expect(fetchFake.mock.calls).toHaveLength(1)
    const init = (fetchFake.mock.calls[0] as unknown as [string, RequestInit])[1]
    expect((init.headers as Record<string, string>).Authorization).toBeUndefined()
  })

  it('accepts rotation owner without a storage, and an explicit undefined storage', () => {
    expect(() => build({ mode: 'bearer', rotation: 'owner' })).not.toThrow()
    expect(() => build({ mode: 'bearer', storage: undefined })).not.toThrow()
    expect(() => build({ mode: 'bearer', storage: undefined, rotation: 'owner' })).not.toThrow()
  })

  it('refuses rotation none without a storage, since there is no owner to read', () => {
    const act = () => build({ mode: 'bearer', rotation: 'none' })

    expect(act).toThrow(TypeError)
    expect(act).toThrow(/auth\.rotation/)
    expect(() => build({ mode: 'bearer', storage: undefined, rotation: 'none' })).toThrow(TypeError)
  })

  it.each([
    ['an invalid string', 'both'],
    ['null', null],
  ])('refuses %s as the rotation without a storage', (_name, rotation) => {
    expect(() => build({ mode: 'bearer', rotation })).toThrow(TypeError)
  })

  it('is a compile error to leave rotation out of a custom storage, or to mix it up', () => {
    const storage = fakeStorage(record(OLD))
    // One short line per case, so that a directive sits on the line of its error.
    const c = (auth: BearerAuthOptions) => createClient({ fetch: vi.fn() as never, auth })
    const thrown = (run: () => unknown) => {
      try {
        run()
      } catch {
        return true
      }
      return false
    }

    // @ts-expect-error a custom storage needs rotation
    const missing = () => c({ mode: 'bearer', storage })
    // @ts-expect-error rotation must be owner or none
    const invalid = () => c({ mode: 'bearer', storage, rotation: 'both' })
    // @ts-expect-error rotation none needs a storage
    const noStorage = () => c({ mode: 'bearer', rotation: 'none' })
    // @ts-expect-error rotation undefined is not a choice
    const undef = () => c({ mode: 'bearer', storage, rotation: undefined })
    // @ts-expect-error null is not a choice
    const nul = () => c({ mode: 'bearer', storage, rotation: null })
    // The valid forms compile without a directive.
    const ok = [
      () => c({ mode: 'bearer', storage, rotation: 'owner' }),
      () => c({ mode: 'bearer', storage, rotation: 'none' }),
      () => c({ mode: 'bearer' }),
      () => c({ mode: 'bearer', rotation: 'owner' }),
    ]

    expect([missing, invalid, noStorage, undef, nul].every(thrown)).toBe(true)
    expect(ok.some(thrown)).toBe(false)
  })
})

describe('signOut', () => {
  it('deletes the device with the token, then clears the storage', async () => {
    const t = setup({ initial: record(OLD) })

    await t.client.signOut()

    const call = t.calls[0]
    expect(call.method).toBe('DELETE')
    expect(call.url).toBe('/api/v1/auth/token')
    expect(call.headers.Authorization).toBe(`Bearer ${OLD}`)
    expect(call.init.credentials).toBe('omit')
    expect(call.headers['X-CSRF-Token']).toBeUndefined()
    expect(t.storage.value).toBeUndefined()
    expect(t.storage.clears).toBe(1)
  })

  it('clears the storage when the call answers an error, and tells the caller', async () => {
    const t = setup({ initial: record(OLD) })
    t.server.signOut = () => answer(500, errorBody('internal', 'bad'))

    const error = await t.client.signOut().catch((e: unknown) => e)

    expect((error as { status: number }).status).toBe(500)
    expect(t.storage.value).toBeUndefined()
  })

  it('clears the storage when the call fails on the network', async () => {
    const t = setup({ initial: record(OLD) })
    t.server.signOut = () => {
      throw new TypeError(`offline ${OLD}`)
    }

    const error = await t.client.signOut().catch((e: unknown) => e)

    expect((error as { code: string }).code).toBe(ErrorCode.network)
    expect(JSON.stringify(error)).not.toContain(OLD)
    expect(t.storage.value).toBeUndefined()
  })

  it('takes a 401 as done: the device is gone already', async () => {
    const hook = vi.fn()
    const t = setup({ initial: record(OLD), onUnauthenticated: hook })
    t.server.signOut = () => answer(401, errorBody('unauthenticated', 'gone'))

    await expect(t.client.signOut()).resolves.toBeUndefined()

    expect(t.storage.value).toBeUndefined()
    expect(hook).not.toHaveBeenCalled()
  })

  it('only clears when there is no token or the stored value is unreadable', async () => {
    const empty = setup()
    await empty.client.signOut()
    expect(empty.calls).toHaveLength(0)
    expect(empty.storage.clears).toBe(1)

    const broken = setup({ initial: 'not json' })
    await broken.client.signOut()
    expect(broken.calls).toHaveLength(0)
    expect(broken.storage.value).toBeUndefined()

    const failing = setup({ initial: record(OLD) })
    failing.storage.failGet = new Error('locked')
    failing.storage.value = record(OLD)
    await failing.client.signOut()
    expect(failing.calls).toHaveLength(0)
  })

  it('throws a typed error when the clear itself fails', async () => {
    const t = setup({ initial: record(OLD) })
    t.storage.failClear = new Error('no')

    const error = await t.client.signOut().catch((e: unknown) => e)

    expect((error as { code: string }).code).toBe(ErrorCode.tokenStorage)
  })
})
