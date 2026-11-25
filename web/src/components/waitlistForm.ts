// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { ApiError, ErrorCode } from '../api/errors'
import type { NamedValues } from '../i18n/plugin'
import { ChallengeError, type ChallengeErrorKind } from '../lib/pow'
import { formatWait } from './contactForm'

type Translate = (key: string, named?: NamedValues) => string

/**
 * The limit of the Go optin package, in code points (optin.MaxEmailRunes). The
 * server decides. This copy only saves a round trip and names the number.
 */
export const WAITLIST_LIMITS = { email: 254 } as const

/**
 * The codes the server puts in `details` as `{field: code}`, each with the
 * catalogue key of its text. optin.CodeRequired and its siblings.
 */
export const WAITLIST_FIELD_CODES = {
  required: 'govue.waitlist.error.required',
  too_long: 'govue.waitlist.error.tooLong',
  invalid_chars: 'govue.waitlist.error.invalidChars',
  non_ascii_email: 'govue.waitlist.error.nonAsciiEmail',
  invalid: 'govue.waitlist.error.invalid',
} as const

/**
 * The consent box is not a field to fill in, so its required code says to
 * agree, as the form's own check does. optin.FieldConsent with CodeRequired.
 */
export const WAITLIST_FIELD_KEYS = {
  consent: { required: 'govue.waitlist.error.consentRequired' },
} as const

/**
 * The codes of the top level error of a refusal of optin's Request route, each
 * with the catalogue key of its text. The first block is optin.Code* in
 * optin/handler.go, the second the codes of the api package the handler also
 * answers with, plus the two the client itself adds (network, timeout).
 * optin.CodeTokenInvalid belongs to the confirm and unsubscribe routes and is
 * never an answer of Request. An `invalid_argument` with `details` is a field
 * error and never reaches this table.
 */
export const WAITLIST_REFUSAL_CODES = {
  cross_origin: 'govue.waitlist.error.refused',
  not_configured: 'govue.waitlist.error.notConfigured',
  pow_required: 'govue.waitlist.error.powRequired',
  pow_invalid: 'govue.waitlist.error.powInvalid',
  pow_expired: 'govue.waitlist.error.powExpired',
  pow_too_fast: 'govue.waitlist.error.powTooFast',
  pow_replay: 'govue.waitlist.error.powReplay',
  pow_busy: 'govue.waitlist.error.powBusy',
  mail_limit: 'govue.waitlist.error.mailLimit',
  method_not_allowed: 'govue.waitlist.error.invalidRequest',
  invalid_argument: 'govue.waitlist.error.invalidRequest',
  unsupported_media_type: 'govue.waitlist.error.invalidRequest',
  request_too_large: 'govue.waitlist.error.tooLarge',
  too_many_requests: 'govue.waitlist.error.rateLimited',
  internal: 'govue.waitlist.error.generic',
  network: 'govue.waitlist.error.network',
  timeout: 'govue.waitlist.error.network',
} as const

/**
 * The refusals that carry a wait, each with the catalogue key of the text that
 * names it. The `{wait}` slot is filled by formatWait, which the contact form
 * shares. Without a usable Retry-After the plain text stays.
 */
export const WAITLIST_WAIT_CODES = {
  too_many_requests: 'govue.waitlist.error.rateLimitedWait',
  mail_limit: 'govue.waitlist.error.mailLimitWait',
  pow_busy: 'govue.waitlist.error.powBusyWait',
} as const

/** The text of a failure of the browser's own proof of work, by kind. */
export const WAITLIST_SOLVER_KEYS: Record<ChallengeErrorKind, string> = {
  salt: 'govue.waitlist.error.solver',
  worker: 'govue.waitlist.error.solver',
  timeout: 'govue.waitlist.error.solver',
  no_number: 'govue.waitlist.error.solver',
  aborted: 'govue.waitlist.error.solver',
}

/** What a code the form does not know falls back to. */
export const WAITLIST_GENERIC_KEY = 'govue.waitlist.error.generic'

/**
 * localizeWaitlistError turns whatever a request threw into the one shape
 * useForm shows, in the visitor's language. The server sends codes, never
 * text: a field code lands on its input, every other refusal becomes one
 * message. A raw error message never reaches the page.
 */
export function localizeWaitlistError(
  caught: unknown,
  t: Translate,
  fields: readonly string[],
): ApiError {
  if (caught instanceof ChallengeError) {
    return new ApiError({
      code: ErrorCode.internal,
      message: t(WAITLIST_SOLVER_KEYS[caught.kind]),
      cause: caught,
    })
  }
  if (!(caught instanceof ApiError)) {
    return new ApiError({
      code: ErrorCode.internal,
      message: t(WAITLIST_GENERIC_KEY),
      cause: caught,
    })
  }
  const base = {
    code: caught.code,
    status: caught.status,
    requestId: caught.requestId,
    retryAfter: caught.retryAfter,
  }

  const details: Record<string, string> = {}
  for (const [field, code] of Object.entries(caught.details)) {
    if (fields.includes(field)) {
      const key =
        (WAITLIST_FIELD_KEYS as Record<string, Record<string, string>>)[field]?.[code] ??
        (WAITLIST_FIELD_CODES as Record<string, string>)[code] ??
        WAITLIST_FIELD_CODES.invalid
      const max = (WAITLIST_LIMITS as Record<string, number>)[field] ?? 0
      details[field] = t(key, { max })
    }
  }
  if (Object.keys(details).length > 0) {
    return new ApiError({ ...base, message: t(WAITLIST_GENERIC_KEY), details })
  }

  const waitKey = (WAITLIST_WAIT_CODES as Record<string, string>)[caught.code]
  if (waitKey && caught.retryAfter !== undefined) {
    return new ApiError({
      ...base,
      message: t(waitKey, { wait: formatWait(caught.retryAfter, t) }),
    })
  }
  const key = (WAITLIST_REFUSAL_CODES as Record<string, string>)[caught.code]
  if (key) {
    return new ApiError({ ...base, message: t(key) })
  }
  // A 403 the server did not name is still a refusal of the origin.
  if (caught.status === 403) {
    return new ApiError({ ...base, message: t(WAITLIST_REFUSAL_CODES.cross_origin) })
  }
  return new ApiError({ ...base, message: t(WAITLIST_GENERIC_KEY) })
}
