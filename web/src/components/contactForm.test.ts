// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'

import { ApiError } from '../api/errors'
import { catalogues, localeCodes } from '../i18n/catalogues'
import type { MessageTree } from '../i18n/messages'
import {
  CONTACT_FIELD_CODES,
  CONTACT_GENERIC_KEY,
  CONTACT_REFUSAL_CODES,
  codePoints,
  isChallengeFailure,
  localizeContactError,
} from './contactForm'

function text(tree: MessageTree, key: string): string | undefined {
  let node: string | MessageTree | undefined = tree
  for (const part of key.split('.')) {
    node = typeof node === 'object' ? node[part] : undefined
  }
  return typeof node === 'string' ? node : undefined
}

describe('the codes of the contact form', () => {
  it('lists the field codes of the Go contact package', () => {
    expect(Object.keys(CONTACT_FIELD_CODES).sort()).toEqual([
      'invalid',
      'invalid_chars',
      'non_ascii_email',
      'required',
      'too_long',
    ])
  })

  it('covers rate limit, challenge failure, unavailable and the rest of the refusals', () => {
    expect(Object.keys(CONTACT_REFUSAL_CODES)).toEqual(
      expect.arrayContaining(['too_many_requests', 'challenge', 'unavailable', 'network']),
    )
  })

  for (const locale of localeCodes) {
    const messages = catalogues[locale].messages
    it(`has a text for every code in ${locale}`, () => {
      const keys = [
        ...Object.values(CONTACT_FIELD_CODES),
        ...Object.values(CONTACT_REFUSAL_CODES),
        CONTACT_GENERIC_KEY,
      ]
      for (const key of keys) {
        expect(text(messages, key), `${locale} ${key}`).toBeTruthy()
        expect(key.startsWith('govue.contact.'), key).toBe(true)
      }
    })
  }
})

const t = (key: string) => `[${key}]`
const fields = ['name', 'email', 'message', 'consent']

describe('localizeContactError', () => {
  it('puts a field code on its field and never shows the server text', () => {
    const error = localizeContactError(
      new ApiError({
        code: 'invalid_argument',
        message: 'name: required',
        status: 400,
        details: { email: 'non_ascii_email', name: 'too_long' },
      }),
      t,
      fields,
    )
    expect(error.details).toEqual({
      email: '[govue.contact.error.nonAsciiEmail]',
      name: '[govue.contact.error.tooLong]',
    })
    expect(error.message).not.toContain('required')
  })

  it('treats a field code it does not know as invalid', () => {
    const error = localizeContactError(
      new ApiError({ code: 'invalid_argument', message: 'x', details: { name: 'new_code' } }),
      t,
      fields,
    )
    expect(error.details).toEqual({ name: '[govue.contact.error.invalid]' })
  })

  it('ignores a detail for a field the form does not have', () => {
    const error = localizeContactError(
      new ApiError({ code: 'invalid_argument', message: 'x', details: { other: 'required' } }),
      t,
      fields,
    )
    expect(error.details).toEqual({})
    expect(error.message).toBe('[govue.contact.error.generic]')
  })

  it('maps each refusal code to its text', () => {
    for (const [code, key] of Object.entries(CONTACT_REFUSAL_CODES)) {
      const error = localizeContactError(new ApiError({ code, message: 'English' }), t, fields)
      expect(error.message, code).toBe(`[${key}]`)
    }
  })

  it('maps a proof of work failure, however the server names it', () => {
    for (const error of [
      new ApiError({ code: 'challenge_expired', message: 'x' }),
      new ApiError({ code: 'invalid_argument', message: 'x', details: { pow: 'expired' } }),
    ]) {
      expect(isChallengeFailure(error)).toBe(true)
      expect(localizeContactError(error, t, fields).message).toBe('[govue.contact.error.challenge]')
    }
  })

  it('turns anything that is not an ApiError into the network text', () => {
    expect(localizeContactError(new Error('boom'), t, fields).message).toBe(
      '[govue.contact.error.network]',
    )
  })

  it('falls back to the generic text for an unknown code', () => {
    expect(
      localizeContactError(new ApiError({ code: 'brand_new', message: 'x' }), t, fields).message,
    ).toBe('[govue.contact.error.generic]')
  })
})

describe('codePoints', () => {
  it('counts runes, not UTF-16 units', () => {
    expect(codePoints('a')).toBe(1)
    expect(codePoints('😀')).toBe(1)
    expect('😀'.length).toBe(2)
  })
})
