// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { ApiError, ErrorCode } from './errors'
import type { RequestOptions } from './client'
import type { TokenStorage } from './tokenStorage'

/** The auth option of a client that sends the session cookie. This is the default. */
export interface CookieAuthOptions {
  mode: 'cookie'
}

/**
 * The auth option of a native app that authenticates with a device token, see
 * ADR 0001. The client sends `Authorization: Bearer <token>`, uses
 * `credentials: 'omit'`, sends no CSRF header and never reads a cookie.
 */
export interface BearerAuthOptions {
  mode: 'bearer'
  /**
   * Where the token is kept. Default: memory only, gone when the app process
   * ends. A product passes an adapter backed by the platform keychain.
   */
  storage?: TokenStorage
  /**
   * Rotate the token when the stored one is older than this. Default 24 hours.
   * The server refuses a rotation inside its own minimum interval, one hour by
   * default, and the client takes that quietly.
   */
  rotateAfterMs?: number
  /** The default device name of signIn. At most 64 bytes, no control characters. */
  deviceName?: string
}

/** What signIn needs: the credentials of the cookie login, same factors. */
export interface SignInCredentials {
  login: string
  password: string
  /** The TOTP code, when the account has one. */
  code?: string
}

export interface SignInOptions {
  /** Overrides the client's device name for this sign in. */
  deviceName?: string
  signal?: AbortSignal
}

/** The device a sign in created. It never carries the token. */
export interface DeviceSession {
  /** The id the device list shows. A handle, not a credential. */
  deviceId: string
  /** The end of the device session, as the server wrote it. A rotation never moves it. */
  expiresAt: string
}

/** What the bearer controller needs from the client's one fetch path. */
export interface Exchange {
  bearer: true
  /** The complete Authorization header value, or none. */
  authorization?: string
  /** Called on a 401 the hook applies to. May be async. */
  onUnauthorized: () => void | Promise<void>
  timeoutMs?: number
}

export type Perform = <T>(
  method: string,
  path: string,
  body: unknown,
  options: RequestOptions | undefined,
  exchange: Exchange,
) => Promise<T>

export interface BearerController {
  request: <T>(method: string, path: string, body: unknown, options?: RequestOptions) => Promise<T>
  signIn: (credentials: SignInCredentials, options?: SignInOptions) => Promise<DeviceSession>
  signOut: () => Promise<void>
}

interface BearerConfig {
  storage: TokenStorage
  rotateAfterMs: number
  deviceName?: string
  onUnauthenticated?: () => void
  perform: Perform
  timeoutMs: number
}

export const DEFAULT_ROTATE_AFTER_MS = 24 * 3_600_000
/** Below the server's rotate timeout of 30 seconds, as ADR 0001 requires. */
const ROTATE_TIMEOUT_MS = 10_000
/** After a quiet refusal the client waits this long before it asks again. */
const REFUSED_BACKOFF_MS = 5 * 60_000
/** session.MaxDeviceNameBytes. */
const MAX_DEVICE_NAME_BYTES = 64
const RECORD_VERSION = 1
const ROTATION_REFUSED = 'rotation_refused'

interface TokenRecord {
  token: string
  /** When the token was issued or last rotated, in ms since the epoch. */
  at: number
}

interface Current {
  record?: TokenRecord
  generation: number
}

const FORBIDDEN_HEADERS = new Set(['authorization', 'x-csrf-token', 'cookie'])

/** checkRotateAfter is the config check of createClient. */
export function checkRotateAfter(value: number): void {
  if (!Number.isFinite(value) || value <= 0) {
    throw new RangeError('auth.rotateAfterMs must be a positive number of milliseconds')
  }
}

/** deviceNameProblem says why a name is refused, or nothing when it is fine. */
export function deviceNameProblem(name: unknown): string | undefined {
  if (typeof name !== 'string') {
    return 'The device name must be text.'
  }
  if (new TextEncoder().encode(name).length > MAX_DEVICE_NAME_BYTES) {
    return `The device name is longer than ${MAX_DEVICE_NAME_BYTES} bytes.`
  }
  // Control and format characters and lone surrogates, the session package refuses them too.
  if (/[\p{Cc}\p{Cf}\p{Cs}]/u.test(name)) {
    return 'The device name has a character that is not allowed.'
  }
  return undefined
}

function storageError(): ApiError {
  // No cause on purpose: an adapter's own error may carry the token.
  return new ApiError({
    code: ErrorCode.tokenStorage,
    message: 'The stored sign in could not be read or written.',
  })
}

function parseRecord(raw: unknown): TokenRecord {
  if (typeof raw !== 'string') {
    throw storageError()
  }
  let value: unknown
  try {
    value = JSON.parse(raw)
  } catch {
    throw storageError()
  }
  const r = value as { v?: unknown; token?: unknown; at?: unknown } | null
  if (
    r === null ||
    typeof r !== 'object' ||
    r.v !== RECORD_VERSION ||
    typeof r.token !== 'string' ||
    r.token === '' ||
    typeof r.at !== 'number' ||
    !Number.isFinite(r.at)
  ) {
    throw storageError()
  }
  return { token: r.token, at: r.at }
}

/** issuedToken reads the token of an Issue or Rotate answer, or nothing. */
function issuedToken(answer: unknown): string | undefined {
  const token = (answer as { token?: unknown } | null | undefined)?.token
  return typeof token === 'string' && token !== '' ? token : undefined
}

function newNonce(): string {
  const bytes = new Uint8Array(16)
  globalThis.crypto.getRandomValues(bytes)
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
}

export function createBearer(config: BearerConfig): BearerController {
  const { storage, rotateAfterMs, perform } = config

  // generation moves whenever the stored token is replaced or cleared. A
  // result that was computed for an older generation is dropped.
  let generation = 0
  // started is true once the first rotation check after start is answered.
  let started = false
  let retryNotBefore = 0
  // The nonce of an attempt whose answer was lost, so the retry reuses it.
  let nonce: string | undefined
  let flight: Promise<Current> | undefined
  let tail: Promise<unknown> = Promise.resolve()

  /** locked runs storage writes one after another. */
  function locked<T>(fn: () => Promise<T>): Promise<T> {
    const run = tail.then(fn)
    tail = run.then(
      () => undefined,
      () => undefined,
    )
    return run
  }

  async function readRecord(): Promise<TokenRecord | undefined> {
    let raw: string | null | undefined
    try {
      raw = await storage.get()
    } catch {
      throw storageError()
    }
    return raw === null || raw === undefined ? undefined : parseRecord(raw)
  }

  async function writeRecord(record: TokenRecord): Promise<void> {
    try {
      await storage.set(JSON.stringify({ v: RECORD_VERSION, token: record.token, at: record.at }))
    } catch {
      throw storageError()
    }
  }

  async function clearStorage(): Promise<void> {
    try {
      await storage.clear()
    } catch {
      throw storageError()
    }
  }

  function notify(): void {
    config.onUnauthenticated?.()
  }

  function due(record: TokenRecord): boolean {
    const now = Date.now()
    if (now < retryNotBefore) {
      return false
    }
    if (!started) {
      return true
    }
    const age = now - record.at
    return age < 0 || age >= rotateAfterMs
  }

  /** rotate is one attempt. It never throws for a failure that keeps the token. */
  async function rotate(record: TokenRecord, gen: number): Promise<Current | 'dropped'> {
    nonce ??= newNonce()
    let answer: unknown
    try {
      answer = await perform<unknown>(
        'POST',
        '/auth/token/rotate',
        { nonce },
        { skipUnauthenticatedHook: true },
        {
          bearer: true,
          authorization: `Bearer ${record.token}`,
          onUnauthorized: () => undefined,
          timeoutMs: Math.min(config.timeoutMs, ROTATE_TIMEOUT_MS),
        },
      )
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) {
        started = true
        nonce = undefined
        if (generation === gen) {
          generation++
          try {
            await locked(clearStorage)
          } finally {
            notify()
          }
        }
        throw error
      }
      if (error instanceof ApiError && error.status === 409 && error.code === ROTATION_REFUSED) {
        // Quiet: the token stays valid and the server says so.
        started = true
        nonce = undefined
        retryNotBefore = Date.now() + REFUSED_BACKOFF_MS
      }
      // A network error, a timeout or a server error keeps the token and
      // the nonce. The next request tries again.
      return { record, generation: gen }
    }

    const token = issuedToken(answer)
    if (token === undefined) {
      return { record, generation: gen }
    }
    const next: TokenRecord = { token, at: Date.now() }
    // The new token is in the storage before anything sends it.
    return locked(async () => {
      if (generation !== gen) {
        return 'dropped' as const
      }
      await writeRecord(next)
      generation++
      started = true
      nonce = undefined
      retryNotBefore = 0
      return { record: next, generation }
    })
  }

  async function resolveCurrent(): Promise<Current> {
    for (let attempt = 0; attempt < 2; attempt++) {
      const gen = generation
      const record = await readRecord()
      if (record === undefined) {
        return { generation: gen }
      }
      if (!due(record)) {
        return { record, generation: gen }
      }
      const result = await rotate(record, gen)
      if (result !== 'dropped') {
        return result
      }
    }
    return { generation }
  }

  /** current is the single flight: concurrent callers share one read and one rotation. */
  function current(): Promise<Current> {
    flight ??= resolveCurrent().finally(() => {
      flight = undefined
    })
    return flight
  }

  async function unauthorized(record: TokenRecord | undefined, gen: number): Promise<void> {
    if (record === undefined) {
      notify()
      return
    }
    if (generation !== gen) {
      return
    }
    generation++
    try {
      await locked(clearStorage)
    } finally {
      notify()
    }
  }

  function ownHeaders(headers: Record<string, string> | undefined): Record<string, string> {
    const out: Record<string, string> = {}
    for (const [name, value] of Object.entries(headers ?? {})) {
      if (!FORBIDDEN_HEADERS.has(name.toLowerCase())) {
        out[name] = value
      }
    }
    return out
  }

  async function request<T>(
    method: string,
    path: string,
    body: unknown,
    options: RequestOptions = {},
  ): Promise<T> {
    const { record, generation: gen } = await current()
    return perform<T>(
      method,
      path,
      body,
      { ...options, headers: ownHeaders(options.headers) },
      {
        bearer: true,
        authorization: record ? `Bearer ${record.token}` : undefined,
        onUnauthorized: () => unauthorized(record, gen),
      },
    )
  }

  function invalidName(): ApiError {
    return new ApiError({
      code: ErrorCode.invalidArgument,
      message: 'The device name is not valid.',
      details: { device_name: 'invalid' },
    })
  }

  async function signIn(
    credentials: SignInCredentials,
    options: SignInOptions = {},
  ): Promise<DeviceSession> {
    const deviceName = options.deviceName ?? config.deviceName
    if (deviceName !== undefined && deviceNameProblem(deviceName) !== undefined) {
      throw invalidName()
    }
    const answer = await perform<unknown>(
      'POST',
      '/auth/token',
      {
        login: credentials.login,
        password: credentials.password,
        code: credentials.code,
        device_name: deviceName,
      },
      { signal: options.signal, skipUnauthenticatedHook: true },
      { bearer: true, onUnauthorized: () => undefined },
    )
    const token = issuedToken(answer)
    if (token === undefined) {
      throw new ApiError({
        code: ErrorCode.internal,
        message: 'The answer did not carry a sign in.',
        status: 200,
      })
    }
    const next: TokenRecord = { token, at: Date.now() }
    // Any rotation or request that is out now belongs to the old sign in.
    generation++
    await locked(() => writeRecord(next))
    started = true
    nonce = undefined
    retryNotBefore = 0
    const info = answer as { token_id?: unknown; expires_at?: unknown }
    return {
      deviceId: typeof info.token_id === 'string' ? info.token_id : '',
      expiresAt: typeof info.expires_at === 'string' ? info.expires_at : '',
    }
  }

  async function signOut(): Promise<void> {
    // Anything that is out now must not write or clear after this point.
    generation++
    let record: TokenRecord | undefined
    try {
      record = await readRecord()
    } catch {
      // Unreadable: there is nothing to tell the server, the clear below is what is left.
    }
    let failure: Error | undefined
    if (record !== undefined) {
      try {
        await perform<unknown>(
          'DELETE',
          '/auth/token',
          undefined,
          { skipUnauthenticatedHook: true },
          {
            bearer: true,
            authorization: `Bearer ${record.token}`,
            onUnauthorized: () => undefined,
          },
        )
      } catch (error) {
        // A 401 means the device is gone already, which is what was asked for.
        if (!(error instanceof ApiError && error.status === 401)) {
          failure =
            error instanceof Error
              ? error
              : new ApiError({ code: ErrorCode.internal, message: 'The sign out failed.' })
        }
      }
    }
    // The local sign out happens whatever the call answered.
    await locked(clearStorage)
    nonce = undefined
    retryNotBefore = 0
    if (failure !== undefined) {
      throw failure
    }
  }

  return { request, signIn, signOut }
}
