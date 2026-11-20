// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { ApiError, ErrorCode } from '../api/errors'
import type { NamedValues } from '../i18n/plugin'

type Translate = (key: string, named?: NamedValues) => string

/** The limits of the Go contact package, in code points. The server decides. */
export const CONTACT_LIMITS = { name: 200, email: 254, message: 5000 } as const

/**
 * The codes the server puts in `details` as `{field: code}`, each with the
 * catalogue key of its text. The Go contact package (#197) defines them.
 */
export const CONTACT_FIELD_CODES = {
  required: 'govue.contact.error.required',
  too_long: 'govue.contact.error.tooLong',
  invalid_chars: 'govue.contact.error.invalidChars',
  non_ascii_email: 'govue.contact.error.nonAsciiEmail',
  invalid: 'govue.contact.error.invalid',
} as const

/**
 * The codes of a refusal that is not about one field, with the catalogue key
 * of the text. They are the api package's codes, plus `challenge` for every
 * proof of work failure (expired, too fast, replayed, wrong, unsigned) and
 * `network` for a request that got no answer.
 */
export const CONTACT_REFUSAL_CODES = {
  too_many_requests: 'govue.contact.error.rateLimited',
  challenge: 'govue.contact.error.challenge',
  unavailable: 'govue.contact.error.unavailable',
  request_too_large: 'govue.contact.error.tooLarge',
  permission_denied: 'govue.contact.error.refused',
  unsupported_media_type: 'govue.contact.error.refused',
  network: 'govue.contact.error.network',
  timeout: 'govue.contact.error.network',
} as const

/** What a code the form does not know falls back to. */
export const CONTACT_GENERIC_KEY = 'govue.contact.error.generic'

/** The `details` keys under which the server reports a failed proof of work. */
const CHALLENGE_DETAIL_KEYS = ['pow', 'challenge', 'solution']

/** True when the refusal means the proof of work, and so the challenge, is spent or wrong. */
export function isChallengeFailure(error: ApiError): boolean {
  return (
    error.code === 'challenge' ||
    error.code.startsWith('challenge_') ||
    error.code.startsWith('pow_') ||
    CHALLENGE_DETAIL_KEYS.some((key) => key in error.details)
  )
}

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
  if (!(caught instanceof ApiError)) {
    return new ApiError({
      code: ErrorCode.network,
      message: t(CONTACT_REFUSAL_CODES.network),
      cause: caught,
    })
  }
  const base = { code: caught.code, status: caught.status, requestId: caught.requestId }

  if (isChallengeFailure(caught)) {
    return new ApiError({ ...base, message: t(CONTACT_REFUSAL_CODES.challenge) })
  }

  const details: Record<string, string> = {}
  for (const [field, code] of Object.entries(caught.details)) {
    if (fields.includes(field)) {
      const key =
        (CONTACT_FIELD_CODES as Record<string, string>)[code] ?? CONTACT_FIELD_CODES.invalid
      details[field] = t(key)
    }
  }
  if (Object.keys(details).length > 0) {
    return new ApiError({ ...base, message: t(CONTACT_GENERIC_KEY), details })
  }

  const key = (CONTACT_REFUSAL_CODES as Record<string, string>)[caught.code] ?? CONTACT_GENERIC_KEY
  return new ApiError({ ...base, message: t(key) })
}
