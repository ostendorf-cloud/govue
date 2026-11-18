// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { ROTATE_TIMEOUT_MS, START_MIN_AGE_MS } from './bearer'
import { createClient } from './client'
import { ErrorCode, isApiError } from './errors'
import type { TokenStorage } from './tokenStorage'

const MIN = 60_000
const HOUR = 60 * MIN
const START = new Date('2026-03-01T10:00:00Z')

// A small model of the server side of ADR 0001: one device with a current and
// a pending token, a retired list, and the revocation of a replayed token. It
// is here so that a test can say who a token belongs to, which a fixed answer
// cannot.
interface Device {
  subject: string
  current: string
  pending?: string
  pendingNonce?: string
  rotatedAt: number
  retired: string[]
  revoked: boolean
}

class Model {
  devices: Device[] = []
  rotateCalls = 0
  private n = 0

  issue(subject: string): string {
    const token = `govue-model-token-${++this.n}-${subject}`
    this.devices.push({
      subject,
      current: token,
      rotatedAt: Date.now(),
      retired: [],
      revoked: false,
    })
    return token
  }

  find(token: string): { device?: Device; slot?: 'current' | 'pending' | 'retired' } {
    for (const device of this.devices) {
      if (device.revoked) continue
      if (device.current === token) return { device, slot: 'current' }
      if (device.pending === token) return { device, slot: 'pending' }
      if (device.retired.includes(token)) return { device, slot: 'retired' }
    }
    return {}
  }

  /** authenticate is a request: the first use of a pending token confirms it. */
  authenticate(token: string): Device | undefined {
    const { device, slot } = this.find(token)
    if (!device) return undefined
    if (slot === 'pending') {
      device.retired.push(device.current)
      device.current = token
      device.pending = undefined
      device.rotatedAt = Date.now()
    }
    return device
  }

  rotate(token: string, nonce: string): { status: number; token?: string } {
    this.rotateCalls++
    const { device, slot } = this.find(token)
    if (!device) return { status: 401 }
    if (slot !== 'current') return { status: 409 }
    if (!device.pending && Date.now() - device.rotatedAt < HOUR) return { status: 409 }
    if (device.pending && nonce !== device.pendingNonce) {
      // Another nonce retires the pending token. Its first use would revoke.
      device.retired.push(device.pending)
    }
    device.pending = `${device.current}-next-${++this.n}`
    device.pendingNonce = nonce
    return { status: 200, token: device.pending }
  }
}

function json(status: number, body?: unknown): Response {
  return new Response(body === undefined ? null : JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

interface ServerHooks {
  /** Holds the rotate answer until the returned function is called. */
  holdRotate?: boolean
  /** Answers the rotation with this status and no token. */
  rotateStatus?: number
  /** Delays the answer of POST /auth/token by this many ms. */
  issueDelayMs?: (login: string) => number
  /** Holds a DELETE /auth/token. */
  holdSignOut?: boolean
}

function modelServer(model: Model, hooks: ServerHooks = {}) {
  const held: Array<() => void> = []
  const heldSignOut: Array<() => void> = []
  const calls: Array<{ url: string; method: string; auth?: string; body?: unknown }> = []
  const fetchFake = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = input as string
    const method = init?.method ?? 'GET'
    const headers = (init?.headers ?? {}) as Record<string, string>
    const auth = headers.Authorization?.replace(/^Bearer /, '')
    const body = typeof init?.body === 'string' ? (JSON.parse(init.body) as { nonce?: string }) : {}
    calls.push({ url, method, auth, body })
    if (url.endsWith('/auth/token/rotate')) {
      if (hooks.holdRotate) {
        await new Promise<void>((resolve) => held.push(resolve))
      }
      if (hooks.rotateStatus) {
        return json(hooks.rotateStatus, { error: { code: 'internal', message: 'x' } })
      }
      const result = model.rotate(auth ?? '', body.nonce ?? '')
      if (result.status === 200)
        return json(200, { token: result.token, token_id: 'd', expires_at: 'e' })
      if (result.status === 409)
        return json(409, { error: { code: 'rotation_refused', message: 'x' } })
      return json(401, { error: { code: 'unauthenticated', message: 'x' } })
    }
    if (url.endsWith('/auth/token') && method === 'POST') {
      const login = (body as unknown as { login: string }).login
      const delay = hooks.issueDelayMs?.(login) ?? 0
      if (delay > 0) await new Promise((resolve) => setTimeout(resolve, delay))
      return json(200, { token: model.issue(login), token_id: 'd', expires_at: 'e' })
    }
    if (url.endsWith('/auth/token') && method === 'DELETE') {
      if (hooks.holdSignOut) {
        await new Promise<void>((resolve) => heldSignOut.push(resolve))
      }
      const { device } = model.find(auth ?? '')
      if (!device) return json(401, { error: { code: 'unauthenticated', message: 'x' } })
      device.revoked = true
      return json(204)
    }
    const device = auth ? model.authenticate(auth) : undefined
    if (!device) return json(401, { error: { code: 'unauthenticated', message: 'x' } })
    return json(200, { subject: device.subject })
  }) as unknown as typeof globalThis.fetch
  return { fetch: fetchFake, held, heldSignOut, calls }
}

interface SlowStorage extends TokenStorage {
  value: () => string | null
}

/** slowStorage answers after a delay, like a keychain plugin does. */
function slowStorage(initial: string | null, getMs: number, setMs: number): SlowStorage {
  let value = initial
  const after = <T>(ms: number, fn: () => T) =>
    new Promise<T>((resolve) => setTimeout(() => resolve(fn()), ms))
  return {
    value: () => value,
    get: () => after(getMs, () => value),
    set: (next) =>
      after(setMs, () => {
        value = next
      }),
    clear: () =>
      after(setMs, () => {
        value = null
      }),
  }
}

function rec(token: string, at: number): string {
  return JSON.stringify({ v: 1, token, at })
}

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms))

function tokenOf(raw: string | null): string {
  return raw === null ? '' : (JSON.parse(raw) as { token: string }).token
}

beforeEach(() => {
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(START)
})

afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
})

/** An account A token that is due and a client on a slow storage. */
function dueAlice(storage: SlowStorage | undefined, hooks: ServerHooks = {}) {
  const model = new Model()
  const token = model.issue('alice')
  // The server counted the issue at START, the token is 25 hours old now.
  vi.setSystemTime(START.getTime() + 25 * HOUR)
  const server = modelServer(model, hooks)
  const slow = storage ?? slowStorage(rec(token, START.getTime()), 1, 20)
  const onUnauthenticated = vi.fn()
  const client = createClient({
    fetch: server.fetch,
    onUnauthenticated,
    auth: { mode: 'bearer', storage: slow },
  })
  return { model, token, server, storage: slow, client, onUnauthenticated }
}

describe('a sign in races a rotation of the old token', () => {
  for (const delay of [0, 1, 2, 5, 10, 15, 19, 25]) {
    it(`ends with the new account's token, request ${delay} ms after the sign in began`, async () => {
      const t = dueAlice(undefined)

      const signing = t.client.signIn({ login: 'bob', password: 'pw' })
      await sleep(delay)
      const request = t.client.get('/me').catch((error: unknown) => error)
      await signing
      await request
      await sleep(80)

      expect(t.model.find(tokenOf(t.storage.value())).device?.subject).toBe('bob')
      await expect(t.client.get('/me')).resolves.toEqual({ subject: 'bob' })
    })
  }

  for (const delay of [3, 8, 14]) {
    it(`gives a request that starts ${delay} ms into the sign in write the new token`, async () => {
      const t = dueAlice(undefined)

      const signing = t.client.signIn({ login: 'bob', password: 'pw' })
      await sleep(delay)
      const request = t.client.get('/me')
      await signing

      await expect(request).resolves.toEqual({ subject: 'bob' })
    })
  }

  it('gives a request that starts during the write the new token also when no rotation is due', async () => {
    const model = new Model()
    const token = model.issue('alice')
    const storage = slowStorage(rec(token, Date.now()), 1, 20)
    const client = createClient({
      fetch: modelServer(model).fetch,
      auth: { mode: 'bearer', storage },
    })

    const signing = client.signIn({ login: 'bob', password: 'pw' })
    await sleep(8)
    const request = client.get('/me')
    await signing

    await expect(request).resolves.toEqual({ subject: 'bob' })
  })

  it('drops the rotation that was out when the sign in wrote', async () => {
    const t = dueAlice(undefined, { holdRotate: true })

    const request = t.client.get('/me').catch((error: unknown) => error)
    await vi.waitFor(() => expect(t.server.held).toHaveLength(1))
    await t.client.signIn({ login: 'bob', password: 'pw' })
    t.server.held[0]()
    await request
    await sleep(80)

    expect(t.model.find(tokenOf(t.storage.value())).device?.subject).toBe('bob')
    await expect(t.client.get('/me')).resolves.toEqual({ subject: 'bob' })
  })

  it('does not overwrite a token that another writer put into the storage', async () => {
    const t = dueAlice(undefined, { holdRotate: true })

    const request = t.client.get('/me').catch((error: unknown) => error)
    await vi.waitFor(() => expect(t.server.held).toHaveLength(1))
    // Not the client: a second context, or the platform, replaced the value.
    const elsewhere = t.model.issue('carol')
    await t.storage.set(rec(elsewhere, Date.now()))
    t.server.held[0]()
    await request
    await sleep(80)

    expect(tokenOf(t.storage.value())).toBe(elsewhere)
  })

  it('hands a request that starts after a sign in the new token, not a flight that began before', async () => {
    // The rotation fails after the sign in, so its flight ends with the old token.
    const t = dueAlice(undefined, { holdRotate: true, rotateStatus: 500 })

    const first = t.client.get('/me').catch((error: unknown) => error)
    await vi.waitFor(() => expect(t.server.held).toHaveLength(1))
    await t.client.signIn({ login: 'bob', password: 'pw' })
    const second = t.client.get('/me')
    t.server.held[0]()
    await first

    await expect(second).resolves.toEqual({ subject: 'bob' })
  })

  it('ends with the token of the sign in whose write came last when two overlap', async () => {
    // Bob's answer is slow, so carol's write is queued first and bob's last.
    const t = dueAlice(undefined, { issueDelayMs: (login) => (login === 'bob' ? 30 : 0) })

    const bob = t.client.signIn({ login: 'bob', password: 'pw' })
    const carol = t.client.signIn({ login: 'carol', password: 'pw' })
    await Promise.all([bob, carol])
    await sleep(80)

    expect(t.model.find(tokenOf(t.storage.value())).device?.subject).toBe('bob')
    await expect(t.client.get('/me')).resolves.toEqual({ subject: 'bob' })
  })
})

describe('a sign out races a rotation', () => {
  it('leaves the storage empty and the late rotation answer unused', async () => {
    const t = dueAlice(slowStorage(null, 1, 1), { holdRotate: true })
    await t.storage.set(rec(t.token, START.getTime()))

    const request = t.client.get('/me').catch((error: unknown) => error)
    await vi.waitFor(() => expect(t.server.held).toHaveLength(1))
    await t.client.signOut()
    t.server.held[0]()
    await request
    await sleep(50)

    expect(t.storage.value()).toBeNull()
    expect(t.model.devices[0].revoked).toBe(true)
    expect(t.onUnauthenticated).not.toHaveBeenCalled()
  })

  it('does not tell the app about a 401 that its own sign out caused', async () => {
    const t = dueAlice(slowStorage(null, 0, 0), { holdSignOut: true })
    t.model.devices[0].rotatedAt = Date.now()
    await t.storage.set(rec(t.token, Date.now()))

    const out = t.client.signOut()
    await vi.waitFor(() => expect(t.server.heldSignOut).toHaveLength(1))
    // The device is gone on the server while the sign out is still running.
    t.model.devices[0].revoked = true
    const request = t.client.get('/me').catch((error: unknown) => error)
    await request
    t.server.heldSignOut[0]()
    await out.catch(() => undefined)

    expect(t.onUnauthenticated).not.toHaveBeenCalled()
  })
})

describe('the rotation timeout', () => {
  it('is below the rotate timeout of the server and below the request timeout', async () => {
    const timeouts: number[] = []
    const real = AbortSignal.timeout.bind(AbortSignal)
    vi.spyOn(AbortSignal, 'timeout').mockImplementation((ms: number) => {
      timeouts.push(ms)
      return real(ms)
    })
    const t = dueAlice(slowStorage(null, 0, 0))
    await t.storage.set(rec(t.token, START.getTime()))

    await t.client.get('/me')

    // The rotation is first, the request second.
    expect(timeouts[0]).toBe(ROTATE_TIMEOUT_MS)
    expect(ROTATE_TIMEOUT_MS).toBeLessThan(30_000)
    expect(timeouts[1]).toBe(15_000)
  })

  it('stays below the server limit when the client timeout is longer', async () => {
    const timeouts: number[] = []
    const real = AbortSignal.timeout.bind(AbortSignal)
    vi.spyOn(AbortSignal, 'timeout').mockImplementation((ms: number) => {
      timeouts.push(ms)
      return real(ms)
    })
    const model = new Model()
    const token = model.issue('alice')
    vi.setSystemTime(START.getTime() + 25 * HOUR)
    const storage = slowStorage(rec(token, START.getTime()), 0, 0)
    const client = createClient({
      fetch: modelServer(model).fetch,
      timeoutMs: 60_000,
      auth: { mode: 'bearer', storage },
    })

    await client.get('/me')

    expect(timeouts[0]).toBeLessThan(30_000)
    expect(timeouts[1]).toBe(60_000)
  })
})

describe('rotation rules', () => {
  it('takes its nonce from the secure random source', async () => {
    const secure = vi.spyOn(globalThis.crypto, 'getRandomValues')
    const insecure = vi.spyOn(Math, 'random')
    const t = dueAlice(slowStorage(null, 0, 0))
    await t.storage.set(rec(t.token, START.getTime()))

    await t.client.get('/me')

    expect(secure).toHaveBeenCalledTimes(1)
    expect((secure.mock.calls[0][0] as Uint8Array).length).toBe(16)
    expect(insecure).not.toHaveBeenCalled()
  })

  it('treats only rotation_refused as the quiet 409', async () => {
    const calls: string[] = []
    const answers = [
      json(409, { error: { code: 'conflict', message: 'x' } }),
      json(409, { error: { code: 'conflict', message: 'x' } }),
    ]
    const storage = slowStorage(rec('govue-test-token-OLD', START.getTime() - 2 * HOUR), 0, 0)
    const client = createClient({
      auth: { mode: 'bearer', storage },
      fetch: ((input: string) => {
        calls.push(input)
        return Promise.resolve(
          input.endsWith('/rotate') ? (answers.shift() ?? json(200, {})) : json(200, {}),
        )
      }) as unknown as typeof fetch,
    })

    await client.get('/a')
    await client.get('/b')

    // No five minute backoff after another 409: the next request asks again.
    expect(calls.filter((c) => c.endsWith('/rotate'))).toHaveLength(2)
  })

  it('rotates a token whose time lies in the future, then backs off five minutes', async () => {
    const model = new Model()
    const token = model.issue('alice')
    const server = modelServer(model)
    const storage = slowStorage(rec(token, Date.now() + 100 * HOUR), 0, 0)
    const client = createClient({ fetch: server.fetch, auth: { mode: 'bearer', storage } })

    for (let minute = 0; minute < 12; minute++) {
      await client.get('/me')
      vi.setSystemTime(Date.now() + MIN)
    }

    // At minute 0, 5 and 10. The server answers every one with a 409.
    expect(model.rotateCalls).toBe(3)
  })

  it('rotates exactly when the interval is reached', async () => {
    const model = new Model()
    const token = model.issue('alice')
    const server = modelServer(model)
    const storage = slowStorage(rec(token, Date.now()), 0, 0)
    const client = createClient({
      fetch: server.fetch,
      auth: { mode: 'bearer', storage, rotateAfterMs: 2 * HOUR },
    })

    await client.get('/start')
    const issuedAt = Date.now()
    vi.setSystemTime(issuedAt + 2 * HOUR - 1)
    await client.get('/before')
    expect(model.rotateCalls).toBe(0)

    vi.setSystemTime(issuedAt + 2 * HOUR)
    await client.get('/at')
    expect(model.rotateCalls).toBe(1)
  })

  it('does not rotate a young token on the first use, and keeps the long interval', async () => {
    const model = new Model()
    const token = model.issue('alice')
    const server = modelServer(model)
    const storage = slowStorage(rec(token, Date.now() - START_MIN_AGE_MS / 2), 0, 0)
    const client = createClient({ fetch: server.fetch, auth: { mode: 'bearer', storage } })

    await client.get('/start')
    expect(model.rotateCalls).toBe(0)
    vi.setSystemTime(Date.now() + 5 * HOUR)
    await client.get('/later')

    expect(model.rotateCalls).toBe(0)
  })

  it('rotates a token that is old enough on the first use', async () => {
    const model = new Model()
    const token = model.issue('alice')
    vi.setSystemTime(START.getTime() + START_MIN_AGE_MS)
    const server = modelServer(model)
    const storage = slowStorage(rec(token, START.getTime()), 0, 0)
    const client = createClient({ fetch: server.fetch, auth: { mode: 'bearer', storage } })

    await client.get('/start')

    expect(model.rotateCalls).toBe(1)
  })

  it('forgets a refusal when a sign in replaces the token', async () => {
    const model = new Model()
    const token = model.issue('alice')
    vi.setSystemTime(START.getTime() + 25 * HOUR)
    const server = modelServer(model)
    const storage = slowStorage(rec(token, START.getTime()), 0, 0)
    const client = createClient({
      fetch: server.fetch,
      auth: { mode: 'bearer', storage, rotateAfterMs: 1000 },
    })
    model.devices[0].rotatedAt = Date.now()

    await client.get('/refused')
    expect(model.rotateCalls).toBe(1)
    await client.signIn({ login: 'bob', password: 'pw' })
    model.devices[1].rotatedAt = Date.now() - 2 * HOUR
    vi.setSystemTime(Date.now() + 2000)
    await client.get('/due')

    expect(model.rotateCalls).toBe(2)
  })
})

describe('a context that does not own the rotation', () => {
  it('never rotates, even with a token that is due, and uses the stored one', async () => {
    const t = dueAlice(slowStorage(null, 0, 0))
    await t.storage.set(rec(t.token, START.getTime()))
    const other = createClient({
      fetch: t.server.fetch,
      auth: { mode: 'bearer', storage: t.storage, rotation: 'none' },
    })

    await other.get('/me')
    vi.setSystemTime(Date.now() + 48 * HOUR)
    await other.get('/me')

    expect(t.model.rotateCalls).toBe(0)
    expect(t.server.calls.every((c) => c.auth === t.token)).toBe(true)
  })

  it('reads the token the owner rotated, and the device stays alive', async () => {
    const t = dueAlice(slowStorage(null, 0, 0))
    await t.storage.set(rec(t.token, START.getTime()))
    const other = createClient({
      fetch: t.server.fetch,
      auth: { mode: 'bearer', storage: t.storage, rotation: 'none' },
    })

    await other.get('/me')
    await t.client.get('/me')
    await other.get('/me')

    expect(t.model.rotateCalls).toBe(1)
    expect(t.model.devices[0].revoked).toBe(false)
    expect(tokenOf(t.storage.value())).not.toBe(t.token)
  })

  it('tells the app about a 401 once per token and leaves the storage alone', async () => {
    const onUnauthenticated = vi.fn()
    const storage = slowStorage(rec('govue-test-token-OLD', Date.now()), 0, 0)
    const clear = vi.spyOn(storage, 'clear')
    const client = createClient({
      onUnauthenticated,
      auth: { mode: 'bearer', storage, rotation: 'none' },
      fetch: () => Promise.resolve(json(401, { error: { code: 'unauthenticated', message: 'x' } })),
    })

    await Promise.allSettled([client.get('/a'), client.get('/b'), client.get('/c')])

    expect(onUnauthenticated).toHaveBeenCalledTimes(1)
    expect(clear).not.toHaveBeenCalled()
    expect(storage.value()).not.toBeNull()
  })

  it('refuses signIn and signOut and a wrong rotation value', async () => {
    const fetchFake = vi.fn()
    const client = createClient({
      fetch: fetchFake,
      auth: { mode: 'bearer', rotation: 'none' },
    })

    const inError = await client.signIn({ login: 'a', password: 'b' }).catch((e: unknown) => e)
    const outError = await client.signOut().catch((e: unknown) => e)

    expect(isApiError(inError)).toBe(true)
    expect(isApiError(outError)).toBe(true)
    expect(fetchFake).not.toHaveBeenCalled()
    expect(() =>
      createClient({
        auth: { mode: 'bearer', rotation: 'both' as never },
        fetch: fetchFake as never,
      }),
    ).toThrow(TypeError)
  })
})

describe('answers that a leak could hide in', () => {
  const SECRET = 'govue-test-token-LEAKCANARY-0123456789abcdef'

  it('keeps the quoted body of a broken JSON answer out of the error', async () => {
    const client = createClient({
      auth: { mode: 'bearer' },
      fetch: () =>
        Promise.resolve(new Response(`{"token":"${SECRET}","token_id":"d",}`, { status: 200 })),
    })

    const error = await client.signIn({ login: 'a', password: 'b' }).catch((e: unknown) => e)

    expect(isApiError(error)).toBe(true)
    expect((error as { cause?: unknown }).cause).toBeUndefined()
    expect(JSON.stringify(error) + String(error) + (error as Error).stack).not.toContain(
      'LEAKCANARY',
    )
  })

  it('turns a failing body read into an ApiError without a cause', async () => {
    const response = json(200, {})
    Object.defineProperty(response, 'text', {
      value: () => Promise.reject(new TypeError(`stream broke ${SECRET}`)),
    })
    const storage = slowStorage(rec(SECRET, Date.now()), 0, 0)
    const client = createClient({
      auth: { mode: 'bearer', storage },
      fetch: () => Promise.resolve(response),
    })

    const error = await client.get('/x').catch((e: unknown) => e)

    expect(isApiError(error)).toBe(true)
    expect((error as { code: string }).code).toBe(ErrorCode.network)
    expect((error as { cause?: unknown }).cause).toBeUndefined()
    expect(String(error) + JSON.stringify(error)).not.toContain('LEAKCANARY')
  })

  it('keeps a Cookie header of the caller off the request', async () => {
    const model = new Model()
    const token = model.issue('alice')
    const server = modelServer(model)
    const storage = slowStorage(rec(token, Date.now()), 0, 0)
    const client = createClient({ fetch: server.fetch, auth: { mode: 'bearer', storage } })
    const spy = vi.fn()
    const wrapped = createClient({
      auth: { mode: 'bearer', storage },
      fetch: ((input: string, init: RequestInit) => {
        spy(init.headers)
        return server.fetch(input, init)
      }) as unknown as typeof fetch,
    })

    await client.get('/me')
    await wrapped.get('/me', { headers: { Cookie: 'sid=1', cookie: 'sid=2' } })

    const headers = spy.mock.calls[0][0] as Record<string, string>
    expect(Object.keys(headers).map((k) => k.toLowerCase())).not.toContain('cookie')
  })
})
