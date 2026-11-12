// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

/** The error shape the Go api package writes, api.WriteError in this module. */
export interface ApiErrorPayload {
  code: string
  message: string
  details?: Record<string, unknown>
  request_id?: string
}

/** The codes the Go api package defines, plus the two this client adds. */
export const ErrorCode = {
  invalidArgument: 'invalid_argument',
  unauthenticated: 'unauthenticated',
  permissionDenied: 'permission_denied',
  notFound: 'not_found',
  conflict: 'conflict',
  tooManyRequests: 'too_many_requests',
  requestTooLarge: 'request_too_large',
  unsupportedMediaType: 'unsupported_media_type',
  methodNotAllowed: 'method_not_allowed',
  unavailable: 'unavailable',
  internal: 'internal',
  /** The request never reached the server, or the answer was not the error shape. */
  network: 'network',
  /** The request was given up on before an answer arrived. */
  timeout: 'timeout',
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

  constructor(init: {
    code: string
    message: string
    status?: number
    details?: Record<string, unknown>
    requestId?: string
    cause?: unknown
  }) {
    super(init.message, { cause: init.cause })
    this.name = 'ApiError'
    this.code = init.code
    this.status = init.status ?? 0
    this.details = stringValues(init.details)
    this.requestId = init.requestId ?? ''
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
