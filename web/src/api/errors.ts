// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

/** The error shape the Go api package writes, api.WriteError in this module. */
export interface ApiErrorPayload {
  code: string
  message: string
  details?: Record<string, unknown>
  request_id?: string
}

/** The codes the Go api package defines, plus the three this client adds. */
export const ErrorCode = {
  invalidArgument: 'invalid_argument',
  unauthenticated: 'unauthenticated',
  permissionDenied: 'permission_denied',
  notFound: 'not_found',
  conflict: 'conflict',
  tooManyRequests: 'too_many_requests',
  requestTooLarge: 'request_too_large',
  unsupportedMediaType: 'unsupported_media_type',
  unavailable: 'unavailable',
  internal: 'internal',
  /** The request never reached the server, or the answer was not the error shape. */
  network: 'network',
  /** The request was given up on before an answer arrived. */
  timeout: 'timeout',
  /** The bearer client could not read, write or clear its token storage. */
  tokenStorage: 'token_storage',
} as const

export type ErrorCodeValue = (typeof ErrorCode)[keyof typeof ErrorCode]

/**
 * ApiError is the single failure type of the client: a refusal from the
 * server, a transport failure and a timeout all arrive as this, so a caller
 * has one thing to catch.
 */
export class ApiError extends Error {
  readonly code: string
  /** The HTTP status, or 0 when nothing was answered. */
  readonly status: number
  /** Field errors, ready to be put on the matching inputs. */
  readonly details: Record<string, string>
  /** The id the log line of this failure carries, for a bug report. */
  readonly requestId: string
  /**
   * The wait the server asked for, in whole seconds, from a `Retry-After`
   * header. Undefined when there was none, when it was not a plain number of
   * seconds, or when it was out of range. A page on another origin cannot read
   * the header unless the server exposes it, and then this stays undefined.
   */
  readonly retryAfter: number | undefined

  constructor(init: {
    code: string
    message: string
    status?: number
    details?: Record<string, unknown>
    requestId?: string
    retryAfter?: number
    cause?: unknown
  }) {
    super(init.message, { cause: init.cause })
    this.name = 'ApiError'
    this.code = init.code
    this.status = init.status ?? 0
    this.details = stringValues(init.details)
    this.requestId = init.requestId ?? ''
    this.retryAfter = validRetryAfter(init.retryAfter)
  }

  /** The login is missing or expired. */
  get isUnauthenticated(): boolean {
    return this.code === ErrorCode.unauthenticated
  }

  /** The request never got an answer, so repeating it is reasonable. */
  get isTransport(): boolean {
    return this.code === ErrorCode.network || this.code === ErrorCode.timeout
  }

  /** A failure the user can do something about, as opposed to a server fault. */
  get isClientFault(): boolean {
    return this.status >= 400 && this.status < 500
  }
}

/** The longest wait a Retry-After may name and still be believed: one day. */
export const MAX_RETRY_AFTER_SECONDS = 86_400

function validRetryAfter(value: number | undefined): number | undefined {
  if (value === undefined || !Number.isInteger(value)) {
    return undefined
  }
  return value >= 1 && value <= MAX_RETRY_AFTER_SECONDS ? value : undefined
}

/**
 * parseRetryAfter reads the delta-seconds form of a Retry-After header: digits
 * only. The HTTP date form, a sign, a fraction, an empty value and anything
 * longer than a day are ignored, so a wrong header never shows a wrong wait.
 */
export function parseRetryAfter(header: string | null): number | undefined {
  if (header === null || !/^\d{1,7}$/.test(header.trim())) {
    return undefined
  }
  return validRetryAfter(Number(header.trim()))
}

/** stringValues keeps only what can be shown next to an input. */
function stringValues(details: Record<string, unknown> | undefined): Record<string, string> {
  const out: Record<string, string> = {}
  for (const [field, value] of Object.entries(details ?? {})) {
    if (typeof value === 'string') {
      out[field] = value
    } else if (typeof value === 'number' || typeof value === 'boolean') {
      out[field] = String(value)
    }
  }
  return out
}

/** isApiError narrows an unknown catch value. */
export function isApiError(error: unknown): error is ApiError {
  return error instanceof ApiError
}
