// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { ApiError, ErrorCode } from '../api/errors'
import type { FieldErrors } from '../composables/useForm'
import type { NamedValues } from '../i18n/plugin'

/**
 * The shortest password the Go account package accepts, account.MinPasswordLength.
 * The server decides. This copy only saves a round trip and names the number.
 */
export const MIN_PASSWORD_LENGTH = 12

type Translate = (key: string, named?: NamedValues) => string

/**
 * requiredErrors is the client side check of a form whose fields all have to be
 * filled in. A value is empty when it holds nothing but whitespace.
 */
export function requiredErrors<T extends object>(
  values: T,
  fields: ReadonlyArray<keyof T & string>,
  t: Translate,
): FieldErrors<T> {
  const errors: FieldErrors<T> = {}
  for (const field of fields) {
    const value = values[field]
    if (typeof value !== 'string' || value.trim() === '') {
      errors[field] = t('govue.account.error.required')
    }
  }
  return errors
}

/** passwordTooShort counts characters the way the server does, by code point. */
export function passwordTooShort(password: string): boolean {
  return [...password].length < MIN_PASSWORD_LENGTH
}

/**
 * localizeError turns whatever a request threw into the one shape useForm
 * shows, in the visitor's language. The server's own message is never shown:
 * it is English, and a text the server chose is not one the page can
 * translate. The code and the field details are what it goes by.
 *
 * A field detail the server sent in a field the form has lands on that field.
 */
export function localizeError(caught: unknown, t: Translate): ApiError {
  if (!(caught instanceof ApiError)) {
    return new ApiError({
      code: ErrorCode.internal,
      message: t('govue.account.error.generic'),
      cause: caught,
    })
  }

  const base = { code: caught.code, status: caught.status, requestId: caught.requestId }

  const fields: Record<string, string> = {}
  for (const [field, detail] of Object.entries(caught.details)) {
    const text = detailText(field, detail, t)
    if (text) {
      fields[field] = text
    }
  }
  if (Object.keys(fields).length > 0) {
    return new ApiError({ ...base, message: t('govue.account.error.generic'), details: fields })
  }

  if (caught.code === ErrorCode.conflict) {
    return new ApiError({
      ...base,
      message: t('govue.account.error.taken'),
      details: { login: t('govue.account.error.taken') },
    })
  }

  return new ApiError({ ...base, message: messageFor(caught, t) })
}

function detailText(field: string, detail: string, t: Translate): string | undefined {
  if (field === 'login' && detail === 'invalid') {
    return t('govue.account.error.loginInvalid')
  }
  if (field === 'password' && detail === 'too_short') {
    return t('govue.account.error.passwordTooShort', { min: MIN_PASSWORD_LENGTH })
  }
  if (field === 'password' && detail === 'too_long') {
    return t('govue.account.error.passwordTooLong')
  }
  return undefined
}

function messageFor(error: ApiError, t: Translate): string {
  switch (error.code) {
    case 'invalid_credentials':
      return t('govue.account.error.invalidCredentials')
    case ErrorCode.unavailable:
      return t('govue.account.error.busy')
    case ErrorCode.tooManyRequests:
      return t('govue.account.error.rateLimited')
    case ErrorCode.permissionDenied:
      return t('govue.account.error.crossOrigin')
    case ErrorCode.network:
    case ErrorCode.timeout:
      return t('govue.account.error.network')
    default:
      return t('govue.account.error.generic')
  }
}
