import { ApiError, ErrorCode, type ApiErrorPayload } from './errors'

// The CSRF header the Go csrf package will read once issue #10 lands;
// matches the old reference implementation's header name, needs a quick
// re-check against the merged package.
const CSRF_HEADER = 'X-CSRF-Token'

const UNSAFE_METHODS = new Set(['POST', 'PUT', 'PATCH', 'DELETE'])

export interface ClientOptions {
  /** Where the versioned API lives. Default /api/v1. */
  baseUrl?: string
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
}

export interface RequestOptions {
  /** Query parameters; undefined and null entries are dropped. */
  query?: Record<string, string | number | boolean | undefined | null>
  headers?: Record<string, string>
  signal?: AbortSignal
}

export interface ApiClient {
  get<T>(path: string, options?: RequestOptions): Promise<T>
  post<T>(path: string, body?: unknown, options?: RequestOptions): Promise<T>
  put<T>(path: string, body?: unknown, options?: RequestOptions): Promise<T>
  patch<T>(path: string, body?: unknown, options?: RequestOptions): Promise<T>
  del<T>(path: string, body?: unknown, options?: RequestOptions): Promise<T>
}

/**
 * createClient is the only place an app calls fetch. It sends the session
 * cookie, attaches the CSRF token to every unsafe method, and turns both a
 * refusal and a transport failure into one ApiError. It never hands back a
 * raw Response.
 */
export function createClient(options: ClientOptions = {}): ApiClient {
  const baseUrl = (options.baseUrl ?? '/api/v1').replace(/\/+$/, '')
  const timeoutMs = options.timeoutMs ?? 15_000
  const doFetch = options.fetch ?? globalThis.fetch.bind(globalThis)

  async function request<T>(
    method: string,
    path: string,
    body: unknown,
    requestOptions: RequestOptions = {},
  ): Promise<T> {
    const headers: Record<string, string> = {
      Accept: 'application/json',
      ...requestOptions.headers,
    }
    if (body !== undefined) {
      headers['Content-Type'] = 'application/json'
    }
    if (UNSAFE_METHODS.has(method)) {
      const token = options.csrfToken?.()
      if (token) {
        headers[CSRF_HEADER] = token
      }
    }

    // The caller's signal and the timeout both have to be able to end the
    // request, and AbortSignal.any is what combines them.
    const timeout = AbortSignal.timeout(timeoutMs)
    const signal = requestOptions.signal
      ? AbortSignal.any([requestOptions.signal, timeout])
      : timeout

    let response: Response
    try {
      response = await doFetch(url(baseUrl, path, requestOptions.query), {
        method,
        headers,
        // The session is a cookie, so it has to travel; cross origin is the
        // server's CORS decision, not this client's.
        credentials: 'same-origin',
        body: body === undefined ? undefined : JSON.stringify(body),
        signal,
      })
    } catch (cause) {
      throw transportError(cause, timeout)
    }

    if (response.status === 401) {
      options.onUnauthenticated?.()
    }
    if (!response.ok) {
      throw await refusal(response)
    }
    if (response.status === 204 || response.headers.get('Content-Length') === '0') {
      return undefined as T
    }
    const text = await response.text()
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
        cause,
      })
    }
  }

  return {
    get: (path, requestOptions) => request('GET', path, undefined, requestOptions),
    post: (path, body, requestOptions) => request('POST', path, body, requestOptions),
    put: (path, body, requestOptions) => request('PUT', path, body, requestOptions),
    patch: (path, body, requestOptions) => request('PATCH', path, body, requestOptions),
    del: (path, body, requestOptions) => request('DELETE', path, body, requestOptions),
  }
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
function transportError(cause: unknown, timeout: AbortSignal): ApiError {
  if (timeout.aborted) {
    return new ApiError({
      code: ErrorCode.timeout,
      message: 'The server took too long to answer. Please try again.',
      cause,
    })
  }
  if (cause instanceof DOMException && cause.name === 'AbortError') {
    // The caller cancelled; it is not a failure to show anybody.
    return new ApiError({ code: ErrorCode.network, message: 'The request was cancelled.', cause })
  }
  return new ApiError({
    code: ErrorCode.network,
    message: 'No connection to the server. Please check your network and try again.',
    cause,
  })
}
