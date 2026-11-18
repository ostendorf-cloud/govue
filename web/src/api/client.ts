// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import {
  checkRotateAfter,
  createBearer,
  DEFAULT_ROTATE_AFTER_MS,
  deviceNameProblem,
  type BearerAuthOptions,
  type BearerController,
  type CookieAuthOptions,
  type DeviceSession,
  type Exchange,
  type SignInCredentials,
  type SignInOptions,
} from './bearer'
import { ApiError, ErrorCode, type ApiErrorPayload } from './errors'
import { createMemoryTokenStorage, type TokenStorage } from './tokenStorage'

export type {
  BearerAuthOptions,
  BearerMemoryAuthOptions,
  BearerStorageAuthOptions,
  CookieAuthOptions,
  DeviceSession,
  SignInCredentials,
  SignInOptions,
} from './bearer'

// The header csrf.HeaderName reads server side.
const CSRF_HEADER = 'X-CSRF-Token'

const UNSAFE_METHODS = new Set(['POST', 'PUT', 'PATCH', 'DELETE'])

export interface ClientOptions {
  /** Where the versioned API lives. Default /api/v1. */
  baseUrl?: string
  /**
   * The credentials mode for every request. Default 'same-origin'. Set to
   * 'include' when the frontend is deployed on a different origin than the
   * API and the server opts into it with middleware.CORSConfig.Credentials.
   */
  credentials?: RequestCredentials
  /**
   * The CSRF token of the current session, read when a request needs it. The
   * app takes it from the page the server rendered, never from a cookie it
   * invented itself.
   */
  csrfToken?: () => string | undefined
  /** How long a request may take. Default 15s. */
  timeoutMs?: number
  /** Replaced in tests. */
  fetch?: typeof globalThis.fetch
  /**
   * Called when the server says the login is gone, so the app can send the
   * browser to the login once instead of every caller handling it.
   */
  onUnauthenticated?: () => void
  /**
   * How the client authenticates. Default `{ mode: 'cookie' }`, the session
   * cookie of a browser. `{ mode: 'bearer' }` is for a native app with a device
   * token and returns a BearerClient. It does not combine with `credentials`
   * or `csrfToken`.
   */
  auth?: CookieAuthOptions | BearerAuthOptions
}

export interface RequestOptions {
  /** Query parameters; undefined and null entries are dropped. */
  query?: Record<string, string | number | boolean | undefined | null>
  headers?: Record<string, string>
  signal?: AbortSignal
  /**
   * Do not call `onUnauthenticated` when this request is answered with 401.
   * Default false. For a request whose 401 is its own answer and not a lost
   * session, such as a failed login.
   */
  skipUnauthenticatedHook?: boolean
}

export interface ApiClient {
  get<T>(path: string, options?: RequestOptions): Promise<T>
  post<T>(path: string, body?: unknown, options?: RequestOptions): Promise<T>
  put<T>(path: string, body?: unknown, options?: RequestOptions): Promise<T>
  patch<T>(path: string, body?: unknown, options?: RequestOptions): Promise<T>
  del<T>(path: string, body?: unknown, options?: RequestOptions): Promise<T>
}

/**
 * The client of a native app. Besides the requests it signs in against
 * `POST /auth/token` and out against `DELETE /auth/token`. It has no method
 * that returns the token.
 */
export interface BearerClient extends ApiClient {
  /**
   * Trades the credentials for a device token, stores it and answers with the
   * device, never with the token. A stored token is replaced.
   */
  signIn(credentials: SignInCredentials, options?: SignInOptions): Promise<DeviceSession>
  /**
   * Ends the device on the server, then clears the storage whatever that call
   * answered. A failed call is thrown after the clear.
   */
  signOut(): Promise<void>
}

/**
 * createClient is the only place an app calls fetch. It sends the session
 * cookie, attaches the CSRF token to every unsafe method, and turns both a
 * refusal and a transport failure into one ApiError. It never hands back a
 * raw Response.
 */
export function createClient(options: ClientOptions & { auth: BearerAuthOptions }): BearerClient
export function createClient(options?: ClientOptions): ApiClient
export function createClient(options: ClientOptions = {}): ApiClient | BearerClient {
  const bearerAuth = options.auth?.mode === 'bearer' ? options.auth : undefined
  // Each option is read once, here. What is checked is what is built, even
  // when the caller passes a getter that answers differently the second time.
  let bearerStorage: TokenStorage | undefined
  let bearerOwner = true
  let bearerRotateAfterMs = DEFAULT_ROTATE_AFTER_MS
  let bearerDeviceName: string | undefined
  if (bearerAuth) {
    if (options.credentials !== undefined || options.csrfToken !== undefined) {
      throw new TypeError('bearer mode does not take the credentials or csrfToken option')
    }
    // First, before anything is built. A JavaScript caller or a cast reaches
    // this without the union type. The messages carry no storage content.
    const rotation: unknown = bearerAuth.rotation
    const storage: TokenStorage | null | undefined = bearerAuth.storage
    const rotateAfterMs = bearerAuth.rotateAfterMs
    const deviceName = bearerAuth.deviceName
    if (storage !== undefined && storage !== null) {
      if (rotation !== 'owner' && rotation !== 'none') {
        throw new TypeError(
          "auth.rotation is required with auth.storage and must be 'owner' or 'none'",
        )
      }
    } else if (rotation !== undefined && rotation !== 'owner') {
      throw new TypeError(
        "auth.rotation must be omitted or 'owner' without auth.storage, 'none' needs the storage of the owner",
      )
    }
    if (rotateAfterMs !== undefined) {
      checkRotateAfter(rotateAfterMs)
    }
    const problem = deviceName === undefined ? undefined : deviceNameProblem(deviceName)
    if (problem) {
      throw new RangeError(problem)
    }
    bearerStorage = storage ?? undefined
    bearerOwner = rotation !== 'none'
    bearerRotateAfterMs = rotateAfterMs ?? DEFAULT_ROTATE_AFTER_MS
    bearerDeviceName = deviceName
  }
  const rawBaseUrl = options.baseUrl ?? '/api/v1'
  // A backward scan, not /\/+$/: the unanchored regex backtracks
  // quadratically on a long run of slashes (CodeQL alert #3).
  let baseUrlEnd = rawBaseUrl.length
  while (baseUrlEnd > 0 && rawBaseUrl[baseUrlEnd - 1] === '/') {
    baseUrlEnd--
  }
  const baseUrl = rawBaseUrl.slice(0, baseUrlEnd)
  const credentials = options.credentials ?? 'same-origin'
  const timeoutMs = options.timeoutMs ?? 15_000
  const doFetch = options.fetch ?? globalThis.fetch.bind(globalThis)

  async function perform<T>(
    method: string,
    path: string,
    body: unknown,
    requestOptions: RequestOptions = {},
    exchange?: Exchange,
  ): Promise<T> {
    const headers: Record<string, string> = {
      Accept: 'application/json',
      ...requestOptions.headers,
    }
    if (body !== undefined) {
      headers['Content-Type'] = 'application/json'
    }
    if (exchange?.authorization) {
      headers.Authorization = exchange.authorization
    }
    if (!exchange && UNSAFE_METHODS.has(method)) {
      const token = options.csrfToken?.()
      if (token) {
        headers[CSRF_HEADER] = token
      }
    }

    // The caller's signal and the timeout both have to be able to end the
    // request, and AbortSignal.any is what combines them.
    const timeout = AbortSignal.timeout(exchange?.timeoutMs ?? timeoutMs)
    const signal = requestOptions.signal
      ? AbortSignal.any([requestOptions.signal, timeout])
      : timeout

    let response: Response
    try {
      response = await doFetch(url(baseUrl, path, requestOptions.query), {
        method,
        headers,
        // The session is a cookie, so it has to travel; cross origin needs
        // both this and the server's CORS decision to agree.
        credentials: exchange ? 'omit' : credentials,
        body: body === undefined ? undefined : JSON.stringify(body),
        signal,
      })
    } catch (cause) {
      throw transportError(cause, timeout, exchange !== undefined)
    }

    if (response.status === 401 && !requestOptions.skipUnauthenticatedHook) {
      if (exchange) {
        await exchange.onUnauthorized()
      } else {
        options.onUnauthenticated?.()
      }
    }
    if (!response.ok) {
      throw await refusal(response)
    }
    if (response.status === 204 || response.headers.get('Content-Length') === '0') {
      return undefined as T
    }
    let text: string
    if (exchange) {
      try {
        text = await response.text()
      } catch {
        // No cause: a stream error may quote the request, and a token with it.
        throw new ApiError({
          code: ErrorCode.network,
          message: 'The answer could not be read. Please try again.',
          status: response.status,
        })
      }
    } else {
      text = await response.text()
    }
    if (text === '') {
      return undefined as T
    }
    try {
      return JSON.parse(text) as T
    } catch (cause) {
      throw new ApiError({
        code: ErrorCode.internal,
        message: 'The answer was not valid JSON.',
        status: response.status,
        // A SyntaxError quotes part of the body, and the body of an Issue or
        // Rotate answer is the token.
        cause: exchange ? undefined : cause,
      })
    }
  }

  let request: <T>(
    method: string,
    path: string,
    body: unknown,
    requestOptions?: RequestOptions,
  ) => Promise<T> = perform
  let bearer: BearerController | undefined
  if (bearerAuth) {
    bearer = createBearer({
      storage: bearerStorage ?? createMemoryTokenStorage(),
      rotateAfterMs: bearerRotateAfterMs,
      deviceName: bearerDeviceName,
      owner: bearerOwner,
      onUnauthenticated: options.onUnauthenticated,
      perform,
      timeoutMs,
    })
    request = bearer.request
  }

  const client: ApiClient = {
    get: (path, requestOptions) => request('GET', path, undefined, requestOptions),
    post: (path, body, requestOptions) => request('POST', path, body, requestOptions),
    put: (path, body, requestOptions) => request('PUT', path, body, requestOptions),
    patch: (path, body, requestOptions) => request('PATCH', path, body, requestOptions),
    del: (path, body, requestOptions) => request('DELETE', path, body, requestOptions),
  }
  if (!bearer) {
    return client
  }
  return { ...client, signIn: bearer.signIn, signOut: bearer.signOut }
}

/** url joins the base and the path and appends the query. */
function url(baseUrl: string, path: string, query: RequestOptions['query']): string {
  const joined = path.startsWith('/') ? `${baseUrl}${path}` : `${baseUrl}/${path}`
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(query ?? {})) {
    if (value !== undefined && value !== null) {
      params.set(key, String(value))
    }
  }
  const search = params.toString()
  return search ? `${joined}?${search}` : joined
}

/** refusal reads the server's error shape, whatever actually came back. */
async function refusal(response: Response): Promise<ApiError> {
  const fallback = {
    code: ErrorCode.internal,
    message: 'Something went wrong. Please try again.',
    status: response.status,
  }
  let payload: { error?: ApiErrorPayload } | undefined
  try {
    payload = (await response.json()) as { error?: ApiErrorPayload }
  } catch {
    // A gateway answering HTML, for instance: the status is all there is.
    return new ApiError(fallback)
  }
  const error = payload?.error
  if (!error || typeof error.code !== 'string' || typeof error.message !== 'string') {
    return new ApiError(fallback)
  }
  return new ApiError({
    code: error.code,
    message: error.message,
    status: response.status,
    details: error.details,
    requestId: error.request_id,
  })
}

/** transportError tells a timeout apart from everything else. */
function transportError(rawCause: unknown, timeout: AbortSignal, dropCause = false): ApiError {
  // A bearer request drops the cause: a transport error may quote the request,
  // and the request carries the token.
  const cause = dropCause ? undefined : rawCause
  if (timeout.aborted) {
    return new ApiError({
      code: ErrorCode.timeout,
      message: 'The server took too long to answer. Please try again.',
      cause,
    })
  }
  if (rawCause instanceof DOMException && rawCause.name === 'AbortError') {
    // The caller cancelled; it is not a failure to show anybody.
    return new ApiError({ code: ErrorCode.network, message: 'The request was cancelled.', cause })
  }
  return new ApiError({
    code: ErrorCode.network,
    message: 'No connection to the server. Please check your network and try again.',
    cause,
  })
}
