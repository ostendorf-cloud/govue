// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { ApiError, ErrorCode } from '../api/errors'
import type { NamedValues } from '../i18n/plugin'
import { ChallengeError, type ChallengeErrorKind } from '../lib/pow'

type Translate = (key: string, named?: NamedValues) => string

/**
 * The limits of the Go contact package, in code points (contact.MaxNameRunes,
 * MaxEmailRunes, MaxMessageRunes). The server decides. This copy only saves a
 * round trip and names the number in the text.
 */
export const CONTACT_LIMITS = { name: 200, email: 254, message: 5000 } as const

/**
 * The codes the server puts in `details` as `{field: code}`, each with the
 * catalogue key of its text. contact.CodeRequired and its siblings.
 */
export const CONTACT_FIELD_CODES = {
  required: 'govue.contact.error.required',
  too_long: 'govue.contact.error.tooLong',
  invalid_chars: 'govue.contact.error.invalidChars',
  non_ascii_email: 'govue.contact.error.nonAsciiEmail',
  invalid: 'govue.contact.error.invalid',
} as const

/**
 * The codes of the top level error of a refusal, each with the catalogue key
 * of its text. The first block is contact.Code* in contact/handler.go, the
 * second the codes of the api package that the contact handlers also answer
 * with, plus the two the client itself adds (network, timeout) and internal.
 * An `invalid_argument` with `details` is a field error and never reaches
 * this table.
 */
export const CONTACT_REFUSAL_CODES = {
  cross_origin: 'govue.contact.error.refused',
  not_configured: 'govue.contact.error.notConfigured',
  pow_required: 'govue.contact.error.powRequired',
  pow_invalid: 'govue.contact.error.powInvalid',
  pow_expired: 'govue.contact.error.powExpired',
  pow_too_fast: 'govue.contact.error.powTooFast',
  pow_replay: 'govue.contact.error.powReplay',
  pow_busy: 'govue.contact.error.powBusy',
  mail_limit: 'govue.contact.error.mailLimit',
  send_failed: 'govue.contact.error.sendFailed',
  method_not_allowed: 'govue.contact.error.invalidRequest',
  invalid_argument: 'govue.contact.error.invalidRequest',
  unsupported_media_type: 'govue.contact.error.invalidRequest',
  request_too_large: 'govue.contact.error.tooLarge',
  too_many_requests: 'govue.contact.error.rateLimited',
  internal: 'govue.contact.error.generic',
  network: 'govue.contact.error.network',
  timeout: 'govue.contact.error.network',
} as const

/**
 * The refusals that carry a wait, each with the catalogue key of the text that
 * names it. The text has a `{wait}` slot, filled by formatWait. Without a
 * usable Retry-After the plain text of CONTACT_REFUSAL_CODES stays.
 */
export const CONTACT_WAIT_CODES = {
  too_many_requests: 'govue.contact.error.rateLimitedWait',
  mail_limit: 'govue.contact.error.mailLimitWait',
  pow_busy: 'govue.contact.error.powBusyWait',
} as const

/**
 * formatWait rounds a wait in seconds up to whole minutes, and to whole hours
 * from an hour on, and names it in the visitor's language: "in about 5
 * minutes". A wait is never rounded down to nothing.
 */
export function formatWait(seconds: number, t: Translate): string {
  const minutes = Math.max(1, Math.ceil(seconds / 60))
  if (minutes < 60) {
    return minutes === 1
      ? t('govue.contact.wait.minute')
      : t('govue.contact.wait.minutes', { n: minutes })
  }
  const hours = Math.round(minutes / 60)
  return hours === 1 ? t('govue.contact.wait.hour') : t('govue.contact.wait.hours', { n: hours })
}

/** The text of a failure of the browser's own proof of work, by kind. */
export const CONTACT_SOLVER_KEYS: Record<ChallengeErrorKind, string> = {
  salt: 'govue.contact.error.solver',
  worker: 'govue.contact.error.solver',
  timeout: 'govue.contact.error.solver',
  no_number: 'govue.contact.error.solver',
  aborted: 'govue.contact.error.solver',
}

/** What a code the form does not know falls back to. */
export const CONTACT_GENERIC_KEY = 'govue.contact.error.generic'

/** codePoints counts the way the server counts: runes, not UTF-16 units. */
export function codePoints(value: string): number {
  return [...value].length
}

/**
 * localizeContactError turns whatever a request threw into the one shape
 * useForm shows, in the visitor's language. The server sends codes, never
 * text: a field code lands on its input, every other refusal becomes one
 * message. A raw error message never reaches the page.
 */
export function localizeContactError(
  caught: unknown,
  t: Translate,
  fields: readonly string[],
): ApiError {
  if (caught instanceof ChallengeError) {
    return new ApiError({
      code: ErrorCode.internal,
      message: t(CONTACT_SOLVER_KEYS[caught.kind]),
      cause: caught,
    })
  }
  if (!(caught instanceof ApiError)) {
    return new ApiError({
      code: ErrorCode.internal,
      message: t(CONTACT_GENERIC_KEY),
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
        (CONTACT_FIELD_CODES as Record<string, string>)[code] ?? CONTACT_FIELD_CODES.invalid
      const max = (CONTACT_LIMITS as Record<string, number>)[field] ?? 0
      details[field] = t(key, { max })
    }
  }
  if (Object.keys(details).length > 0) {
    return new ApiError({ ...base, message: t(CONTACT_GENERIC_KEY), details })
  }

  const waitKey = (CONTACT_WAIT_CODES as Record<string, string>)[caught.code]
  if (waitKey && caught.retryAfter !== undefined) {
    return new ApiError({
      ...base,
      message: t(waitKey, { wait: formatWait(caught.retryAfter, t) }),
    })
  }
  const key = (CONTACT_REFUSAL_CODES as Record<string, string>)[caught.code]
  if (key) {
    return new ApiError({ ...base, message: t(key) })
  }
  // A 403 the server did not name is still a refusal of the origin.
  if (caught.status === 403) {
    return new ApiError({ ...base, message: t(CONTACT_REFUSAL_CODES.cross_origin) })
  }
  return new ApiError({ ...base, message: t(CONTACT_GENERIC_KEY) })
}
