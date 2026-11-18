// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { computed, ref, toValue, type ComputedRef, type MaybeRefOrGetter, type Ref } from 'vue'

import type { LoginResult, Passkey } from '../api/account'
import type { ApiClient } from '../api/client'
import { ApiError, ErrorCode } from '../api/errors'

/** The route prefix of the Go account package's passkey handlers. */
export const DEFAULT_PASSKEY_PATH = '/account/passkeys'

/**
 * Why a passkey call failed. The first six are the codes of the server
 * contract (or the client's own answer for `unsupported`). The rest name a
 * failure the contract leaves to the status or to the browser.
 */
export type PasskeyErrorCode =
  | 'reauthentication_required'
  | 'invalid_passkey'
  | 'invalid_credentials'
  | 'last_factor'
  | 'not_found'
  | 'unsupported'
  | 'unauthenticated'
  | 'invalid_argument'
  | 'permission_denied'
  | 'rate_limited'
  | 'unavailable'
  | 'network'
  | 'already_registered'
  | 'busy'
  | 'bearer_unsupported'
  | 'failed'

/**
 * The names of the browser errors that `reason` can carry. A name outside
 * this fixed set becomes `other`, so `reason` is never free text.
 */
const REASONS = new Set([
  'SecurityError',
  'NotSupportedError',
  'ConstraintError',
  'InvalidStateError',
  'UnknownError',
  'DataError',
  'EncodingError',
  'NotReadableError',
  'TypeError',
])

/**
 * The one failure type of usePasskey. The message is a fixed English text per
 * code. It never holds a value from the server, the browser or a credential,
 * and the error carries no cause, because a cause may quote a challenge.
 */
export class PasskeyError extends Error {
  readonly code: PasskeyErrorCode
  /** The HTTP status, or 0 when the failure was not an answer. */
  readonly status: number
  /** The id the server log line of this failure carries. */
  readonly requestId: string
  /**
   * For an operator: the name of the browser error behind a `failed`, such as
   * `SecurityError` for a wrong RP id or domain. One of a fixed set or
   * `other`, never the browser's message. Empty when the browser was not the
   * source.
   */
  readonly reason: string

  constructor(
    code: PasskeyErrorCode,
    init: { status?: number; requestId?: string; reason?: string } = {},
  ) {
    super(MESSAGES[code])
    this.name = 'PasskeyError'
    this.code = code
    this.status = init.status ?? 0
    this.requestId = init.requestId ?? ''
    this.reason = init.reason === undefined ? '' : REASONS.has(init.reason) ? init.reason : 'other'
  }
}

const MESSAGES: Record<PasskeyErrorCode, string> = {
  reauthentication_required: 'A recent login is needed. Log in again and retry.',
  invalid_passkey: 'The passkey could not be registered.',
  invalid_credentials: 'The passkey could not be verified.',
  last_factor: 'The last sign in factor cannot be removed.',
  not_found: 'The passkey was not found.',
  unsupported: 'This browser does not support passkeys.',
  unauthenticated: 'The login is missing or expired.',
  invalid_argument: 'The request was not valid.',
  permission_denied: 'The request was refused.',
  rate_limited: 'Too many attempts.',
  unavailable: 'The server is busy.',
  network: 'No connection to the server.',
  already_registered: 'This device already holds a passkey for the account.',
  busy: 'Another passkey ceremony is running.',
  bearer_unsupported: 'A client with a device token cannot run a passkey ceremony.',
  failed: 'The passkey ceremony failed.',
}

/**
 * idle: nothing ran. busy: a ceremony is in flight. done: the last one went
 * through. cancelled: the person dismissed the prompt, which is no error.
 * error: the last one failed, see `error`.
 */
export type PasskeyStatus = 'idle' | 'busy' | 'done' | 'cancelled' | 'error'

export interface PasskeyOptions {
  /** The client of createClient. It carries the session and the challenge cookie. */
  client: MaybeRefOrGetter<ApiClient>
  /** The route prefix, relative to the client's base URL. */
  path?: MaybeRefOrGetter<string | undefined>
}

export interface PasskeyLoginOptions {
  /**
   * Ask for the browser's autofill UI (`mediation: 'conditional'`) instead of
   * a modal prompt. It needs a form field with `autocomplete="... webauthn"`.
   * A conditional login leaves `status`, `busy` and `error` alone, so it can
   * wait in the background while the rest of the page works. It yields to a
   * modal ceremony: it resolves with undefined, like a cancel, and sends no
   * request while one runs, and a modal `enrol` or `login` that starts ends
   * it the same way. The product restarts it when it wants it again.
   */
  conditional?: boolean
  /**
   * Ends the ceremony early. A conditional one the app no longer wants. It
   * aborts the begin request and the browser step, not the finish request,
   * which runs to its answer once the browser step has returned.
   */
  signal?: AbortSignal
}

export interface PasskeyState {
  /** Whether this browser has the native JSON helpers. Fixed at creation. */
  supported: Ref<boolean>
  status: Readonly<Ref<PasskeyStatus>>
  busy: ComputedRef<boolean>
  /** The last failure, cleared when the next ceremony starts. */
  error: Readonly<Ref<PasskeyError | undefined>>
  /**
   * Registers a passkey for the signed in account. Resolves with the passkey,
   * or with undefined when the person dismissed the prompt. Rejects with a
   * PasskeyError. A second call of this instance while its own `enrol` runs
   * gets the same promise. Any other call while a ceremony runs on the page,
   * through this or another instance, rejects with `busy`.
   */
  enrol: (name?: string) => Promise<Passkey | undefined>
  /**
   * Logs in with a passkey. Resolves with the login answer, or with undefined
   * when the person dismissed the prompt. Rejects with a PasskeyError.
   */
  login: (options?: PasskeyLoginOptions) => Promise<LoginResult | undefined>
  list: () => Promise<Passkey[]>
  remove: (id: string) => Promise<void>
  /** Back to idle with no error. */
  reset: () => void
}

/**
 * isPasskeySupported is true when the browser has what the ceremony needs:
 * the native JSON helpers of WebAuthn level 3 and navigator.credentials.
 * It is false on the server, so a prerendered page never shows a passkey
 * control. No library fills the gap.
 */
export function isPasskeySupported(): boolean {
  if (typeof window === 'undefined' || typeof navigator === 'undefined') {
    return false
  }
  const pkc = (globalThis as { PublicKeyCredential?: Partial<typeof PublicKeyCredential> })
    .PublicKeyCredential
  return (
    pkc !== undefined &&
    typeof pkc.parseCreationOptionsFromJSON === 'function' &&
    typeof pkc.parseRequestOptionsFromJSON === 'function' &&
    typeof navigator.credentials?.create === 'function' &&
    typeof navigator.credentials?.get === 'function'
  )
}

/** The person dismissed the prompt, or the ceremony was aborted. Not an error. */
class Cancelled extends Error {
  constructor() {
    super('cancelled')
    this.name = 'Cancelled'
  }
}

/** The content of a begin answer. */
interface Begin<T> {
  public_key?: T
}

/**
 * isBearerClient tells a client of a native app with a device token from the
 * cookie client: only `createClient({ auth: { mode: 'bearer' } })` returns
 * `signIn` and `signOut`. Such a client sends no cookie, so the challenge
 * cookie of a begin answer never comes back, and a passkey login starts a
 * cookie session, not a device token.
 */
export function isBearerClient(client: ApiClient): boolean {
  return 'signIn' in client && 'signOut' in client
}

/**
 * The ceremony state of the page. There is one challenge cookie per browser
 * and a new begin replaces it, so one ceremony may run per page, whatever the
 * number of usePasskey instances and buttons. `modal` is the enrolment or the
 * login with a prompt. `waiting` is a conditional login that waits for the
 * autofill UI, and it yields to every modal ceremony.
 */
let modal: { owner: object; kind: 'enrol' | 'login'; promise: Promise<unknown> } | undefined
let waiting: AbortController | undefined

export function usePasskey(options: PasskeyOptions): PasskeyState {
  const supported = ref(isPasskeySupported())
  const status = ref<PasskeyStatus>('idle')
  const error = ref<PasskeyError | undefined>()
  const busy = computed(() => status.value === 'busy')
  // The identity of this instance: it may share the promise of its own call.
  const owner = {}

  const client = () => toValue(options.client)
  const base = () => toValue(options.path) ?? DEFAULT_PASSKEY_PATH

  /**
   * One request. A failure becomes a PasskeyError without a cause. A 401 on
   * the login routes is the answer to the ceremony, not a lost session, so
   * those requests do not call the client's onUnauthenticated.
   */
  async function request<T>(
    method: 'get' | 'post',
    path: string,
    body: unknown,
    ownAnswer: boolean,
    signal?: AbortSignal,
  ): Promise<T> {
    try {
      const requestOptions = { skipUnauthenticatedHook: ownAnswer, signal }
      return method === 'get'
        ? await client().get<T>(`${base()}${path}`, requestOptions)
        : await client().post<T>(`${base()}${path}`, body, requestOptions)
    } catch (caught) {
      throw fromApiError(caught)
    }
  }

  function requireSupported(): void {
    if (isBearerClient(client())) {
      throw new PasskeyError('bearer_unsupported')
    }
    if (!isPasskeySupported()) {
      throw new PasskeyError('unsupported')
    }
  }

  function exclusive<T>(kind: 'enrol' | 'login', work: () => Promise<T>): Promise<T | undefined> {
    if (modal) {
      if (modal.owner === owner && modal.kind === kind) {
        return modal.promise as Promise<T | undefined>
      }
      return Promise.reject(new PasskeyError('busy'))
    }
    // A prompt the person asked for ends the autofill ceremony that waits.
    waiting?.abort()
    status.value = 'busy'
    error.value = undefined
    const promise = (async () => {
      try {
        const value = await work()
        status.value = 'done'
        return value
      } catch (caught) {
        if (caught instanceof Cancelled) {
          status.value = 'cancelled'
          return undefined
        }
        const failure = toPasskeyError(caught)
        error.value = failure
        status.value = 'error'
        throw failure
      } finally {
        modal = undefined
      }
    })()
    modal = { owner, kind, promise }
    return promise
  }

  async function enrolCeremony(name: string | undefined): Promise<Passkey> {
    requireSupported()
    const begun = await request<Begin<PublicKeyCredentialCreationOptionsJSON>>(
      'post',
      '/register/begin',
      {},
      false,
    )
    let publicKey: PublicKeyCredentialCreationOptions
    try {
      publicKey = PublicKeyCredential.parseCreationOptionsFromJSON(requirePublicKey(begun))
    } catch {
      throw new PasskeyError('failed')
    }
    const credential = await ceremonyStep('enrol', () =>
      navigator.credentials.create({ publicKey }),
    )
    const body: { credential: unknown; name?: string } = { credential: credentialJson(credential) }
    if (name !== undefined) {
      body.name = name
    }
    const answer = await request<{ passkey?: Passkey }>('post', '/register/finish', body, false)
    if (!answer?.passkey) {
      throw new PasskeyError('failed')
    }
    return answer.passkey
  }

  async function loginCeremony(loginOptions: PasskeyLoginOptions): Promise<LoginResult> {
    requireSupported()
    if (loginOptions.conditional) {
      const available = (
        PublicKeyCredential as { isConditionalMediationAvailable?: () => Promise<boolean> }
      ).isConditionalMediationAvailable
      if (typeof available === 'function' && !(await available.call(PublicKeyCredential))) {
        throw new PasskeyError('unsupported')
      }
    }
    const begun = await request<Begin<PublicKeyCredentialRequestOptionsJSON>>(
      'post',
      '/login/begin',
      {},
      true,
      loginOptions.signal,
    )
    let publicKey: PublicKeyCredentialRequestOptions
    try {
      publicKey = PublicKeyCredential.parseRequestOptionsFromJSON(requirePublicKey(begun))
    } catch {
      throw new PasskeyError('failed')
    }
    const credential = await ceremonyStep('login', () =>
      navigator.credentials.get({
        publicKey,
        ...(loginOptions.conditional ? { mediation: 'conditional' as const } : {}),
        ...(loginOptions.signal ? { signal: loginOptions.signal } : {}),
      }),
    )
    const answer = await request<LoginResult>(
      'post',
      '/login/finish',
      { credential: credentialJson(credential) },
      true,
    )
    if (!answer || typeof answer.login !== 'string') {
      throw new PasskeyError('failed')
    }
    return answer
  }

  function enrol(name?: string): Promise<Passkey | undefined> {
    return exclusive('enrol', () => enrolCeremony(name))
  }

  async function login(loginOptions: PasskeyLoginOptions = {}): Promise<LoginResult | undefined> {
    if (loginOptions.conditional) {
      // A modal ceremony owns the challenge cookie: the autofill one yields
      // and resolves as cancelled, without a request.
      if (modal) {
        return undefined
      }
      // A second conditional ceremony replaces the first one.
      waiting?.abort()
      const own = new AbortController()
      waiting = own
      const signal = loginOptions.signal
        ? AbortSignal.any([own.signal, loginOptions.signal])
        : own.signal
      try {
        return await loginCeremony({ ...loginOptions, signal })
      } catch (caught) {
        // Ended from outside: by a modal ceremony, a newer conditional one
        // or the caller's signal. That is a cancel, whatever the abort
        // broke, the begin request included.
        if (caught instanceof Cancelled || signal.aborted) {
          return undefined
        }
        throw toPasskeyError(caught)
      } finally {
        if (waiting === own) {
          waiting = undefined
        }
      }
    }
    return exclusive('login', () => loginCeremony(loginOptions))
  }

  async function list(): Promise<Passkey[]> {
    const answer = await request<{ passkeys?: Passkey[] }>('get', '', undefined, false)
    return Array.isArray(answer?.passkeys) ? answer.passkeys : []
  }

  async function remove(id: string): Promise<void> {
    await request<void>('post', '/delete', { id }, false)
  }

  function reset(): void {
    if (status.value !== 'busy') {
      status.value = 'idle'
      error.value = undefined
    }
  }

  return { supported, status, busy, error, enrol, login, list, remove, reset }
}

function requirePublicKey<T>(begun: Begin<T> | undefined): T {
  if (!begun || typeof begun.public_key !== 'object' || begun.public_key === null) {
    throw new PasskeyError('failed')
  }
  return begun.public_key
}

/** credentialJson serialises with the native toJSON, never by hand. */
function credentialJson(credential: Credential): unknown {
  const candidate = credential as { toJSON?: () => unknown }
  if (typeof candidate.toJSON !== 'function') {
    throw new PasskeyError('unsupported')
  }
  return candidate.toJSON()
}

/**
 * ceremonyStep runs one navigator.credentials call and sorts what the browser
 * throws. A dismissed prompt is Cancelled. Nothing the browser says is kept:
 * its messages may quote the options.
 */
async function ceremonyStep(
  kind: 'enrol' | 'login',
  call: () => Promise<Credential | null>,
): Promise<Credential> {
  let credential: Credential | null
  try {
    credential = await call()
  } catch (caught) {
    const name = errorName(caught)
    switch (name) {
      case 'NotAllowedError':
      case 'AbortError':
        throw new Cancelled()
      case 'InvalidStateError':
        // Only create throws it for an excluded authenticator.
        throw new PasskeyError(kind === 'enrol' ? 'already_registered' : 'failed', {
          reason: name,
        })
      default:
        throw new PasskeyError('failed', { reason: name })
    }
  }
  if (credential === null) {
    throw new Cancelled()
  }
  return credential
}

function errorName(caught: unknown): string {
  if (typeof caught === 'object' && caught !== null && 'name' in caught) {
    return String(caught.name)
  }
  return ''
}

const SERVER_CODES = new Set<string>([
  'reauthentication_required',
  'invalid_passkey',
  'invalid_credentials',
  'last_factor',
  'not_found',
  'unauthenticated',
  'invalid_argument',
  'permission_denied',
])

/** fromApiError maps the client's failure onto the passkey codes, dropping the cause. */
function fromApiError(caught: unknown): PasskeyError {
  if (caught instanceof PasskeyError) {
    return caught
  }
  if (!(caught instanceof ApiError)) {
    return new PasskeyError('failed')
  }
  const init = { status: caught.status, requestId: caught.requestId }
  if (SERVER_CODES.has(caught.code)) {
    return new PasskeyError(caught.code as PasskeyErrorCode, init)
  }
  switch (caught.code) {
    case ErrorCode.network:
    case ErrorCode.timeout:
      return new PasskeyError('network', init)
    case ErrorCode.tooManyRequests:
      return new PasskeyError('rate_limited', init)
    case ErrorCode.unavailable:
      return new PasskeyError('unavailable', init)
    default:
      // The CSRF middleware answers 403 as plain text, so the client has no
      // code for it. It is the refusal of a cross origin request.
      return new PasskeyError(caught.status === 403 ? 'permission_denied' : 'failed', init)
  }
}

function toPasskeyError(caught: unknown): PasskeyError {
  return caught instanceof PasskeyError ? caught : new PasskeyError('failed')
}
