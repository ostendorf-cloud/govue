// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { beforeEach, describe, expect, it, vi } from 'vitest'

import { createClient } from './client'
import type { ApiError } from './errors'
import { ErrorCode, isApiError } from './errors'

/** answer builds a Response the fake fetch hands back. */
function answer(status: number, body?: unknown, headers: Record<string, string> = {}): Response {
  const init: ResponseInit = { status, headers: { 'Content-Type': 'application/json', ...headers } }
  if (body === undefined) {
    return new Response(null, init)
  }
  return new Response(JSON.stringify(body), init)
}

/** errorBody is what the Go api package writes. */
function errorBody(code: string, message: string, extra: Record<string, unknown> = {}) {
  return { error: { code, message, ...extra } }
}

describe('createClient', () => {
  let calls: Array<{ url: string; init: RequestInit }>
  let respond: (url: string, init: RequestInit) => Promise<Response>

  const fetchStub = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
    calls.push({ url, init: init ?? {} })
    return respond(url, init ?? {})
  }) as unknown as typeof globalThis.fetch

  beforeEach(() => {
    calls = []
    respond = () => Promise.resolve(answer(200, { ok: true }))
  })

  it('calls the versioned path and returns the decoded body', async () => {
    respond = () => Promise.resolve(answer(200, { id: 7, title: 'lamp' }))
    const client = createClient({ fetch: fetchStub })

    const thing = await client.get<{ id: number; title: string }>('/things/7')

    expect(thing).toEqual({ id: 7, title: 'lamp' })
    expect(calls[0].url).toBe('/api/v1/things/7')
    expect(calls[0].init.method).toBe('GET')
    expect(calls[0].init.credentials).toBe('same-origin')
  })

  it('sends include credentials when configured for a cross-origin deployment', async () => {
    const client = createClient({ fetch: fetchStub, credentials: 'include' })

    await client.get('/things')

    expect(calls[0].init.credentials).toBe('include')
  })

  it('appends the query and drops empty entries', async () => {
    const client = createClient({ fetch: fetchStub })

    await client.get('/things', {
      query: { search: 'lamp', limit: 20, archived: false, cursor: undefined },
    })

    expect(calls[0].url).toBe('/api/v1/things?search=lamp&limit=20&archived=false')
  })

  it('takes its own base url', async () => {
    const client = createClient({ fetch: fetchStub, baseUrl: 'https://api.example.test/v2/' })

    await client.get('things')

    expect(calls[0].url).toBe('https://api.example.test/v2/things')
  })

  it('trims a long run of trailing slashes from its own base url', async () => {
    const client = createClient({
      fetch: fetchStub,
      baseUrl: 'https://api.example.test/v2' + '/'.repeat(10000),
    })

    await client.get('things')

    expect(calls[0].url).toBe('https://api.example.test/v2/things')
  })

  it('attaches the CSRF token to unsafe methods only', async () => {
    const client = createClient({ fetch: fetchStub, csrfToken: () => 'token-42' })

    await client.get('/things')
    await client.post('/things', { title: 'lamp' })
    await client.put('/things/7', { title: 'lamp' })
    await client.patch('/things/7', { title: 'lamp2' })
    await client.del('/things/7')

    const header = (index: number) =>
      (calls[index].init.headers as Record<string, string>)['X-CSRF-Token']
    expect(header(0)).toBeUndefined()
    expect(header(1)).toBe('token-42')
    expect(header(2)).toBe('token-42')
    expect(header(3)).toBe('token-42')
    expect(header(4)).toBe('token-42')
  })

  it('omits the CSRF header when no token is available', async () => {
    const client = createClient({ fetch: fetchStub, csrfToken: () => undefined })

    await client.post('/things', { title: 'lamp' })

    expect((calls[0].init.headers as Record<string, string>)['X-CSRF-Token']).toBeUndefined()
  })

  it('sends a JSON body only when there is one', async () => {
    const client = createClient({ fetch: fetchStub })

    await client.post('/things', { title: 'lamp' })
    await client.post('/things/7/archive')

    expect(calls[0].init.body).toBe('{"title":"lamp"}')
    expect((calls[0].init.headers as Record<string, string>)['Content-Type']).toBe(
      'application/json',
    )
    expect(calls[1].init.body).toBeUndefined()
    expect((calls[1].init.headers as Record<string, string>)['Content-Type']).toBeUndefined()
  })

  it('returns nothing for a 204', async () => {
    respond = () => Promise.resolve(answer(204))
    const client = createClient({ fetch: fetchStub })

    await expect(client.del('/things/7')).resolves.toBeUndefined()
  })

  it('returns nothing for an empty 200 body', async () => {
    respond = () => Promise.resolve(new Response('', { status: 200 }))
    const client = createClient({ fetch: fetchStub })

    await expect(client.post('/things/7/archive')).resolves.toBeUndefined()
  })

  it('maps the error shape onto ApiError', async () => {
    respond = () =>
      Promise.resolve(
        answer(
          400,
          errorBody('invalid_argument', 'field title must not be empty', {
            details: { title: 'must not be empty', count: 3, ignored: { nested: true } },
            request_id: 'req-99',
          }),
        ),
      )
    const client = createClient({ fetch: fetchStub })

    const error = await client.post('/things', {}).catch((caught: unknown) => caught)

    expect(isApiError(error)).toBe(true)
    const apiError = error as ApiError
    expect(apiError.code).toBe(ErrorCode.invalidArgument)
    expect(apiError.message).toBe('field title must not be empty')
    expect(apiError.status).toBe(400)
    expect(apiError.requestId).toBe('req-99')
    // Only what can be shown next to an input survives.
    expect(apiError.details).toEqual({ title: 'must not be empty', count: '3' })
    expect(apiError.isClientFault).toBe(true)
    expect(apiError.isTransport).toBe(false)
  })

  it('maps a refusal with no details or request id', async () => {
    respond = () => Promise.resolve(answer(404, errorBody('not_found', 'thing not found')))
    const client = createClient({ fetch: fetchStub })

    const error = (await client.get('/things/7').catch((caught: unknown) => caught)) as ApiError

    expect(error.code).toBe(ErrorCode.notFound)
    expect(error.details).toEqual({})
    expect(error.requestId).toBe('')
  })

  it('falls back when the answer is not the error shape', async () => {
    respond = () => Promise.resolve(new Response('<html>502</html>', { status: 502 }))
    const client = createClient({ fetch: fetchStub })

    const error = (await client.get('/things').catch((caught: unknown) => caught)) as ApiError

    expect(error.code).toBe(ErrorCode.internal)
    expect(error.status).toBe(502)
    expect(error.message).not.toContain('html')
  })

  it('reports a lost login once, through the callback', async () => {
    respond = () => Promise.resolve(answer(401, errorBody('unauthenticated', 'please log in')))
    const onUnauthenticated = vi.fn()
    const client = createClient({ fetch: fetchStub, onUnauthenticated })

    const error = (await client.get('/things').catch((caught: unknown) => caught)) as ApiError

    expect(error.isUnauthenticated).toBe(true)
    expect(onUnauthenticated).toHaveBeenCalledTimes(1)
  })

  it('turns a failed connection into a transport error', async () => {
    respond = () => Promise.reject(new TypeError('Failed to fetch'))
    const client = createClient({ fetch: fetchStub })

    const error = (await client.get('/things').catch((caught: unknown) => caught)) as ApiError

    expect(error.code).toBe(ErrorCode.network)
    expect(error.isTransport).toBe(true)
    expect(error.status).toBe(0)
  })

  it('gives up after the timeout', async () => {
    respond = (_url, init) =>
      new Promise((_resolve, reject) => {
        init.signal?.addEventListener('abort', () => {
          reject(new DOMException('aborted', 'AbortError'))
        })
      })
    const client = createClient({ fetch: fetchStub, timeoutMs: 10 })

    const error = (await client.get('/things').catch((caught: unknown) => caught)) as ApiError

    expect(error.code).toBe(ErrorCode.timeout)
    expect(error.isTransport).toBe(true)
  })

  it("passes the caller's cancellation through", async () => {
    respond = (_url, init) =>
      new Promise((_resolve, reject) => {
        init.signal?.addEventListener('abort', () => {
          reject(new DOMException('aborted', 'AbortError'))
        })
      })
    const controller = new AbortController()
    const client = createClient({ fetch: fetchStub })

    const pending = client.get('/things', { signal: controller.signal })
    controller.abort()
    const error = (await pending.catch((caught: unknown) => caught)) as ApiError

    expect(error.code).toBe(ErrorCode.network)
    expect(error.message).toContain('cancelled')
  })
})
