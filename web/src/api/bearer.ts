// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { ApiError, ErrorCode } from './errors'
import type { RequestOptions } from './client'
import type { TokenStorage } from './tokenStorage'

/** The auth option of a client that sends the session cookie. This is the default. */
export interface CookieAuthOptions {
  mode: 'cookie'
}

/** What every bearer client takes, whatever its storage. */
interface BearerAuthBase {
  mode: 'bearer'
  /**
   * Rotate the token when the stored one is older than this. Default 24 hours.
   * The server refuses a rotation inside its own minimum interval, one hour by
   * default, and the client takes that quietly.
   */
  rotateAfterMs?: number
  /** The default device name of signIn. At most 64 bytes, no control characters. */
  deviceName?: string
}

/**
 * A bearer client on the default in memory storage. Nobody else can read that
 * storage, so this client is always the owner and `rotation` stays optional.
 */
export interface BearerMemoryAuthOptions extends BearerAuthBase {
  /** Memory only, gone when the app process ends. */
  storage?: undefined
  /** Always 'owner' here. A 'none' client reads the storage of an owner and needs `storage`. */
  rotation?: 'owner'
}

/**
 * A bearer client on a storage the product passes, such as one backed by the
 * platform keychain. Another context can share that storage, so the product
 * must say whether this client rotates.
 */
export interface BearerStorageAuthOptions extends BearerAuthBase {
  /** Where the token is kept. */
  storage: TokenStorage
  /**
   * Required with a `storage`, no default. Exactly one context per storage is
   * the owner, 'owner': it rotates, signs in, signs out and clears the storage
   * on a 401. Every other context that reads the same storage, such as an app
   * extension or a background runner, is 'none'. It never rotates, uses the
   * stored token as it is, and on a 401 calls onUnauthenticated and leaves the
   * storage to the owner. Two rotating contexts revoke the device, since the
   * server takes the second rotation as a replay (ADR 0001), and a forgotten
   * option would make a second owner. A 'none' client refuses signIn and
   * signOut.
   */
  rotation: 'owner' | 'none'
}

/**
 * The auth option of a native app that authenticates with a device token, see
 * ADR 0001. The client sends `Authorization: Bearer <token>`, uses
 * `credentials: 'omit'`, sends no CSRF header and never reads a cookie.
 *
 * A union, so that `storage` and `rotation` go together: a custom storage needs
 * a `rotation`, and without a storage only 'owner' makes sense.
 */
export type BearerAuthOptions = BearerMemoryAuthOptions | BearerStorageAuthOptions

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
  /** True for the one context that rotates. */
  owner: boolean
  onUnauthenticated?: () => void
  perform: Perform
  timeoutMs: number
}

export const DEFAULT_ROTATE_AFTER_MS = 24 * 3_600_000
/**
 * The first use after start rotates only a token at least this old, the
 * default minimum interval of the server. A younger token would get a 409.
 */
export const START_MIN_AGE_MS = 3_600_000
/** Below the server's rotate timeout of 30 seconds, as ADR 0001 requires. */
export const ROTATE_TIMEOUT_MS = 10_000
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

/** A consistent read: the stored token and the generation it belongs to. */
interface Snapshot {
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
  const { storage, rotateAfterMs, perform, owner } = config

  // generation moves, inside the lock, with every change of the stored token:
  // a sign in, a sign out, a rotation, a clear after a 401. A result that was
  // computed for an older generation is dropped.
  let generation = 0
  // started is true once the first rotation check after start is answered.
  let started = false
  let retryNotBefore = 0
  // The nonce of an attempt whose answer was lost, so the retry reuses it.
  let nonce: string | undefined
  // How many sign outs are running. A 401 during one is its own doing.
  let signingOut = 0
  // The token a non owner told the app about already.
  let notifiedFor: string | undefined
  let flight: Promise<Snapshot> | undefined
  let tail: Promise<unknown> = Promise.resolve()

  /**
   * locked runs every storage read and write one after another. A read that
   * starts while a write is pending waits for it, so a read never sees a value
   * the write behind it is about to replace.
   */
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
    if (signingOut === 0) {
      config.onUnauthenticated?.()
    }
  }

  /** snapshot reads the token and the generation together, behind pending writes. */
  function snapshot(): Promise<Snapshot> {
    return locked(async () => ({ record: await readRecord(), generation }))
  }

  function due(record: TokenRecord): boolean {
    const now = Date.now()
    if (now < retryNotBefore) {
      return false
    }
    const age = now - record.at
    if (age < 0) {
      // A clock that moved back. Rotating re-stamps the token.
      return true
    }
    return age >= (started ? rotateAfterMs : Math.min(START_MIN_AGE_MS, rotateAfterMs))
  }

  /**
   * dropToken clears the storage for a 401 of the token of generation gen.
   * The generation check and the clear are one step inside the lock, so of
   * many 401s one clears and one tells the app.
   */
  async function dropToken(gen: number): Promise<void> {
    let mine = false
    try {
      await locked(async () => {
        if (generation !== gen) {
          return
        }
        mine = true
        generation++
        await clearStorage()
      })
    } finally {
      if (mine) {
        notify()
      }
    }
  }

  /** rotate is one attempt. It never throws for a failure that keeps the token. */
  async function rotate(record: TokenRecord, gen: number): Promise<Snapshot | 'dropped'> {
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
        await dropToken(gen)
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
    // The new token is in the storage before anything sends it. Inside the
    // lock the storage is read again: it must still hold the token that was
    // rotated, and nothing may have replaced it since. Else the result is
    // another sign in's to overwrite, and it is dropped.
    return locked(async () => {
      if (generation !== gen) {
        return 'dropped' as const
      }
      const stored = await readRecord()
      if (stored?.token !== record.token) {
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

  async function resolveCurrent(): Promise<Snapshot> {
    for (let attempt = 0; attempt < 3; attempt++) {
      const snap = await snapshot()
      if (!owner || snap.record === undefined) {
        return snap
      }
      if (!due(snap.record)) {
        started = true
        return snap
      }
      if (attempt === 2) {
        // Dropped twice: something keeps replacing the token. Use what is there.
        return snap
      }
      const result = await rotate(snap.record, snap.generation)
      if (result !== 'dropped') {
        return result
      }
    }
    return snapshot()
  }

  /** current is the single flight: concurrent callers share one read and one rotation. */
  function current(): Promise<Snapshot> {
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
    if (!owner) {
      // The owner decides about the storage. Tell the app once per token.
      if (notifiedFor !== record.token) {
        notifiedFor = record.token
        notify()
      }
      return
    }
    await dropToken(gen)
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
    let snap = await current()
    // A flight that began before a sign in or a rotation finished may hand
    // out the token it replaced. Ask again, a read costs little.
    for (let again = 0; again < 2 && snap.generation !== generation; again++) {
      snap = await current()
    }
    const { record, generation: gen } = snap
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

  function notOwner(): ApiError {
    return new ApiError({
      code: ErrorCode.invalidArgument,
      message: 'This context does not own the sign in.',
    })
  }

  async function signIn(
    credentials: SignInCredentials,
    options: SignInOptions = {},
  ): Promise<DeviceSession> {
    if (!owner) {
      throw notOwner()
    }
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
    // One step inside the lock: the write and the new generation. A read
    // behind it sees the new token with the new generation, and a rotation of
    // the old token that is out now is dropped. Two sign ins end with the one
    // whose write came last.
    await locked(async () => {
      await writeRecord(next)
      generation++
      started = true
      nonce = undefined
      retryNotBefore = 0
      notifiedFor = undefined
    })
    const info = answer as { token_id?: unknown; expires_at?: unknown }
    return {
      deviceId: typeof info.token_id === 'string' ? info.token_id : '',
      expiresAt: typeof info.expires_at === 'string' ? info.expires_at : '',
    }
  }

  async function signOut(): Promise<void> {
    if (!owner) {
      throw notOwner()
    }
    signingOut++
    try {
      // The generation moves first, so that a rotation that is out now drops
      // its result instead of writing a token the clear below would miss.
      const record = await locked(async () => {
        generation++
        try {
          return await readRecord()
        } catch {
          // Unreadable: nothing to tell the server, the clear below is what is left.
          return undefined
        }
      })
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
      await locked(async () => {
        generation++
        nonce = undefined
        retryNotBefore = 0
        notifiedFor = undefined
        await clearStorage()
      })
      if (failure !== undefined) {
        throw failure
      }
    } finally {
      signingOut--
    }
  }

  return { request, signIn, signOut }
}
