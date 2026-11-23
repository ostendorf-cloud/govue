// @vitest-environment node
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

import { ApiError } from '../api/errors'
import { catalogues, localeCodes } from '../i18n/catalogues'
import type { MessageTree } from '../i18n/messages'
import { ChallengeError } from '../lib/pow'
import {
  CONTACT_FIELD_CODES,
  CONTACT_GENERIC_KEY,
  CONTACT_LIMITS,
  CONTACT_REFUSAL_CODES,
  CONTACT_SOLVER_KEYS,
  CONTACT_WAIT_CODES,
  codePoints,
  formatWait,
  localizeContactError,
} from './contactForm'

function text(tree: MessageTree, key: string): string | undefined {
  let node: string | MessageTree | undefined = tree
  for (const part of key.split('.')) {
    node = typeof node === 'object' ? node[part] : undefined
  }
  return typeof node === 'string' ? node : undefined
}

/**
 * Every code of contact/handler.go, written out. The first block is the
 * contact.Code* constants, the second the api codes the handlers answer with.
 * Source: the handler of PR #207 (contact/handler.go, lines 29 to 41).
 */
const HANDLER_CODES = [
  'cross_origin',
  'not_configured',
  'pow_required',
  'pow_invalid',
  'pow_expired',
  'pow_too_fast',
  'pow_replay',
  'pow_busy',
  'mail_limit',
  'send_failed',
  'method_not_allowed',
  'invalid_argument',
  'unsupported_media_type',
  'request_too_large',
  'too_many_requests',
]
const FIELD_CODES = ['required', 'too_long', 'invalid_chars', 'non_ascii_email', 'invalid']

describe('the codes of the contact form', () => {
  it('has a text key for every code of the handler', () => {
    for (const code of HANDLER_CODES) {
      expect(Object.keys(CONTACT_REFUSAL_CODES), code).toContain(code)
    }
  })

  it('has a text key for every field code of the validation', () => {
    expect(Object.keys(CONTACT_FIELD_CODES).sort()).toEqual([...FIELD_CODES].sort())
  })

  // Once the Go package is merged into this repository, its own constants are
  // read, so a new code there fails this test until it has a text.
  const handler = fileURLToPath(new URL('../../../contact/handler.go', import.meta.url))
  const contact = fileURLToPath(new URL('../../../contact/contact.go', import.meta.url))
  // The Go sources are in this repository: a missing file fails, it never skips.
  it('has a text for every code constant of contact/handler.go', () => {
    const source = readFileSync(handler, 'utf8')
    const codes = [...source.matchAll(/^\s*Code\w+\s*=\s*"([a-z_]+)"/gm)].map((m) => m[1])
    expect(codes.length).toBeGreaterThan(5)
    for (const code of codes) {
      expect(Object.keys(CONTACT_REFUSAL_CODES), code).toContain(code)
    }
  })
  it('has a text for every field code of contact/contact.go', () => {
    const source = readFileSync(contact, 'utf8')
    const codes = [...source.matchAll(/^\s*Code\w+\s*=\s*"([a-z_]+)"/gm)].map((m) => m[1])
    expect(codes.length).toBeGreaterThan(3)
    for (const code of codes) {
      expect(Object.keys(CONTACT_FIELD_CODES), code).toContain(code)
    }
  })

  for (const locale of localeCodes) {
    const messages = catalogues[locale].messages
    it(`has a text for every code in ${locale}`, () => {
      const keys = [
        ...Object.values(CONTACT_FIELD_CODES),
        ...Object.values(CONTACT_REFUSAL_CODES),
        ...Object.values(CONTACT_SOLVER_KEYS),
        ...Object.values(CONTACT_WAIT_CODES),
        'govue.contact.unavailable',
        'govue.contact.wait.minute',
        'govue.contact.wait.minutes',
        'govue.contact.wait.hour',
        'govue.contact.wait.hours',
        CONTACT_GENERIC_KEY,
      ]
      for (const key of keys) {
        expect(text(messages, key), `${locale} ${key}`).toBeTruthy()
        expect(key.startsWith('govue.contact.'), key).toBe(true)
      }
    })

    it(`gives every pow code its own text in ${locale}`, () => {
      const pow = Object.entries(CONTACT_REFUSAL_CODES).filter(([code]) => code.startsWith('pow_'))
      const texts = pow.map(([, key]) => text(messages, key))
      expect(new Set(texts).size).toBe(pow.length)
    })

    it(`ships no consent text in ${locale}: the site writes it`, () => {
      expect(text(messages, 'govue.contact.consent')).toBeUndefined()
    })

    it(`has the {wait} slot in every wait text in ${locale}`, () => {
      for (const key of Object.values(CONTACT_WAIT_CODES)) {
        expect(text(messages, key), key).toContain('{wait}')
      }
    })

    it(`names the limit and the special characters in ${locale}`, () => {
      expect(text(messages, CONTACT_FIELD_CODES.too_long)).toContain('{max}')
      expect(text(messages, CONTACT_FIELD_CODES.non_ascii_email)).toMatch(/ä.*ö.*ü/)
    })
  }
})

const t = (key: string, named?: Record<string, string | number>) =>
  `[${key}${named ? JSON.stringify(named) : ''}]`
const fields = ['name', 'email', 'message', 'consent']

describe('localizeContactError with the real codes', () => {
  it('puts a field code on its field and never shows the server text', () => {
    const error = localizeContactError(
      new ApiError({
        code: 'invalid_argument',
        message: 'some fields are invalid',
        status: 400,
        details: { email: 'non_ascii_email', name: 'too_long' },
      }),
      t,
      fields,
    )
    expect(error.details).toEqual({
      email: '[govue.contact.error.nonAsciiEmail{"max":254}]',
      name: '[govue.contact.error.tooLong{"max":200}]',
    })
    expect(error.message).not.toContain('some fields')
  })

  it('says to agree for a consent field the server refused, and keeps the other fields', () => {
    const error = localizeContactError(
      new ApiError({
        code: 'invalid_argument',
        message: 'some fields are invalid',
        status: 400,
        details: { consent: 'required', name: 'required' },
      }),
      t,
      fields,
    )
    expect(error.details).toEqual({
      consent: '[govue.contact.error.consentRequired{"max":0}]',
      name: '[govue.contact.error.required{"max":200}]',
    })
  })

  it('treats a field code it does not know as invalid', () => {
    const error = localizeContactError(
      new ApiError({ code: 'invalid_argument', message: 'x', details: { name: 'new_code' } }),
      t,
      fields,
    )
    expect(error.details).toEqual({ name: '[govue.contact.error.invalid{"max":200}]' })
  })

  it('treats invalid_argument without details as a refused request', () => {
    const error = localizeContactError(
      new ApiError({ code: 'invalid_argument', message: 'body is not a valid submission' }),
      t,
      fields,
    )
    expect(error.message).toBe('[govue.contact.error.invalidRequest]')
  })

  // The pow_ rule: every proof of work code has its own text, so a rule that
  // lumps them together or drops them fails here.
  const expected: Record<string, string> = {
    cross_origin: 'refused',
    not_configured: 'notConfigured',
    pow_required: 'powRequired',
    pow_invalid: 'powInvalid',
    pow_expired: 'powExpired',
    pow_too_fast: 'powTooFast',
    pow_replay: 'powReplay',
    pow_busy: 'powBusy',
    mail_limit: 'mailLimit',
    send_failed: 'sendFailed',
    too_many_requests: 'rateLimited',
    request_too_large: 'tooLarge',
    unsupported_media_type: 'invalidRequest',
    method_not_allowed: 'invalidRequest',
  }
  for (const [code, key] of Object.entries(expected)) {
    it(`maps ${code} to ${key}`, () => {
      const error = localizeContactError(new ApiError({ code, message: 'English' }), t, fields)
      expect(error.message).toBe(`[govue.contact.error.${key}]`)
    })
  }

  it('names the wait of a refusal that carries a Retry-After', () => {
    for (const [code, key] of Object.entries(CONTACT_WAIT_CODES)) {
      const error = localizeContactError(
        new ApiError({ code, message: 'English', retryAfter: 300 }),
        t,
        fields,
      )
      expect(error.message).toBe(`[${key}{"wait":"[govue.contact.wait.minutes{\\"n\\":5}]"}]`)
      expect(error.retryAfter).toBe(300)
    }
  })

  it('keeps the plain text without a Retry-After, and for a code with no wait text', () => {
    expect(
      localizeContactError(new ApiError({ code: 'too_many_requests', message: 'x' }), t, fields)
        .message,
    ).toBe('[govue.contact.error.rateLimited]')
    expect(
      localizeContactError(
        new ApiError({ code: 'send_failed', message: 'x', retryAfter: 300 }),
        t,
        fields,
      ).message,
    ).toBe('[govue.contact.error.sendFailed]')
  })

  it('maps a 403 with a code it does not know to the refused text', () => {
    const error = localizeContactError(
      new ApiError({ code: 'forbidden_by_proxy', message: 'x', status: 403 }),
      t,
      fields,
    )
    expect(error.message).toBe('[govue.contact.error.refused]')
  })

  it('shows its own text for a failure of the proof of work in the browser', () => {
    for (const kind of ['salt', 'worker', 'timeout', 'no_number', 'aborted'] as const) {
      const error = localizeContactError(new ChallengeError(kind, 'x'), t, fields)
      expect(error.message, kind).toBe('[govue.contact.error.solver]')
      expect(error.message).not.toContain('network')
    }
  })

  it('maps the transport codes of the client to the network text', () => {
    for (const code of ['network', 'timeout']) {
      expect(localizeContactError(new ApiError({ code, message: 'x' }), t, fields).message).toBe(
        '[govue.contact.error.network]',
      )
    }
  })

  it('falls back to the generic text for an unknown code and for anything else', () => {
    expect(
      localizeContactError(new ApiError({ code: 'brand_new', message: 'x' }), t, fields).message,
    ).toBe('[govue.contact.error.generic]')
    expect(localizeContactError(new Error('boom'), t, fields).message).toBe(
      '[govue.contact.error.generic]',
    )
  })
})

describe('formatWait', () => {
  const rows: Array<[number, string]> = [
    [1, '[govue.contact.wait.minute]'],
    [60, '[govue.contact.wait.minute]'],
    [61, '[govue.contact.wait.minutes{"n":2}]'],
    [300, '[govue.contact.wait.minutes{"n":5}]'],
    [3540, '[govue.contact.wait.minutes{"n":59}]'],
    [3600, '[govue.contact.wait.hour]'],
    [3601, '[govue.contact.wait.hours{"n":2}]'],
    [5340, '[govue.contact.wait.hours{"n":2}]'],
    [7200, '[govue.contact.wait.hours{"n":2}]'],
    [86_400, '[govue.contact.wait.hours{"n":24}]'],
  ]
  for (const [seconds, expected] of rows) {
    it(`rounds ${seconds} s up to ${expected}`, () => {
      expect(formatWait(seconds, t)).toBe(expected)
    })
  }
})

describe('the limits', () => {
  it('are the ones of the Go package', () => {
    expect(CONTACT_LIMITS).toEqual({ name: 200, email: 254, message: 5000 })
  })

  it('count runes, not UTF-16 units', () => {
    expect(codePoints('a')).toBe(1)
    expect(codePoints('😀')).toBe(1)
    expect('😀'.length).toBe(2)
  })
})
