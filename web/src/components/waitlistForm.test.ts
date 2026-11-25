// @vitest-environment node
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

import { ApiError } from '../api/errors'
import { catalogues, formalCatalogues, localeCodes } from '../i18n/catalogues'
import type { MessageTree } from '../i18n/messages'
import { ChallengeError } from '../lib/pow'
import {
  WAITLIST_FIELD_CODES,
  WAITLIST_FIELD_KEYS,
  WAITLIST_GENERIC_KEY,
  WAITLIST_LIMITS,
  WAITLIST_REFUSAL_CODES,
  WAITLIST_SOLVER_KEYS,
  WAITLIST_WAIT_CODES,
  localizeWaitlistError,
} from './waitlistForm'

function text(tree: MessageTree, key: string): string | undefined {
  let node: string | MessageTree | undefined = tree
  for (const part of key.split('.')) {
    node = typeof node === 'object' ? node[part] : undefined
  }
  return typeof node === 'string' ? node : undefined
}

/**
 * Every code the Request route of optin/handler.go answers with, written out
 * from govue PR #270, which is not on main yet. The first block is the optin
 * Code* constants, the second the api codes the handler uses.
 */
const REQUEST_CODES = [
  'cross_origin',
  'not_configured',
  'pow_required',
  'pow_invalid',
  'pow_expired',
  'pow_too_fast',
  'pow_replay',
  'pow_busy',
  'mail_limit',
  'method_not_allowed',
  'invalid_argument',
  'unsupported_media_type',
  'request_too_large',
  'too_many_requests',
  'internal',
]
/** optin.CodeTokenInvalid: an answer of Confirm and Unsubscribe only. */
const NOT_REQUEST_CODES = ['token_invalid']
/** The field codes of optin/mail.go. */
const FIELD_CODES = ['required', 'too_long', 'invalid_chars', 'non_ascii_email', 'invalid']

/** Every catalogue a visitor can get: each language and each formal variant. */
const trees: Array<[string, MessageTree]> = [
  ...localeCodes.map((code): [string, MessageTree] => [code, catalogues[code].messages]),
  ...Object.entries(formalCatalogues).map(([code, tree]): [string, MessageTree] => [
    `${code} formal`,
    tree,
  ]),
]

/** The keys the component itself passes to t(), read from its source. */
const componentSource = readFileSync(
  fileURLToPath(new URL('./GovueWaitlistForm.vue', import.meta.url)),
  'utf8',
)
const componentKeys = [...componentSource.matchAll(/t\('(govue\.[\w.]+)'/g)].map((m) => m[1])

describe('the codes of the waitlist form', () => {
  it('has a text key for every code of the Request route', () => {
    for (const code of REQUEST_CODES) {
      expect(Object.keys(WAITLIST_REFUSAL_CODES), code).toContain(code)
    }
  })

  it('maps no code that Request never answers', () => {
    for (const code of NOT_REQUEST_CODES) {
      expect(Object.keys(WAITLIST_REFUSAL_CODES), code).not.toContain(code)
    }
  })

  it('has a text key for every field code of the address check', () => {
    expect(Object.keys(WAITLIST_FIELD_CODES).sort()).toEqual([...FIELD_CODES].sort())
  })

  it('reads the keys of the component from its source', () => {
    expect(componentKeys.length).toBeGreaterThan(10)
    expect(componentKeys).toContain('govue.waitlist.success')
  })

  for (const [name, messages] of trees) {
    it(`has a text for every key in ${name}`, () => {
      const keys = [
        ...Object.values(WAITLIST_FIELD_CODES),
        ...Object.values(WAITLIST_FIELD_KEYS).flatMap((codes) => Object.values(codes)),
        ...Object.values(WAITLIST_REFUSAL_CODES),
        ...Object.values(WAITLIST_SOLVER_KEYS),
        ...Object.values(WAITLIST_WAIT_CODES),
        ...componentKeys,
        // formatWait, shared with the contact form.
        'govue.contact.wait.minute',
        'govue.contact.wait.minutes',
        'govue.contact.wait.hour',
        'govue.contact.wait.hours',
        WAITLIST_GENERIC_KEY,
      ]
      for (const key of keys) {
        expect(text(messages, key), `${name} ${key}`).toBeTruthy()
      }
    })

    it(`gives every pow code its own text in ${name}`, () => {
      const pow = Object.entries(WAITLIST_REFUSAL_CODES).filter(([code]) => code.startsWith('pow_'))
      const texts = pow.map(([, key]) => text(messages, key))
      expect(new Set(texts).size).toBe(pow.length)
    })

    it(`ships no consent text in ${name}: the site writes it`, () => {
      expect(text(messages, 'govue.waitlist.consent')).toBeUndefined()
    })

    it(`has the {wait} slot in every wait text in ${name}`, () => {
      for (const key of Object.values(WAITLIST_WAIT_CODES)) {
        expect(text(messages, key), key).toContain('{wait}')
      }
    })
  }
})

const t = (key: string, named?: Record<string, string | number>) =>
  `[${key}${named ? JSON.stringify(named) : ''}]`
const fields = ['email', 'consent']

describe('localizeWaitlistError with the real codes', () => {
  it('puts a field code on its field and never shows the server text', () => {
    const error = localizeWaitlistError(
      new ApiError({
        code: 'invalid_argument',
        message: 'some fields are invalid',
        status: 400,
        details: { email: 'too_long' },
      }),
      t,
      fields,
    )
    expect(error.details).toEqual({ email: '[govue.waitlist.error.tooLong{"max":254}]' })
    expect(error.message).not.toContain('some fields')
  })

  for (const code of FIELD_CODES) {
    it(`maps the address code ${code} to its own text`, () => {
      const error = localizeWaitlistError(
        new ApiError({ code: 'invalid_argument', message: 'x', details: { email: code } }),
        t,
        fields,
      )
      expect(error.details.email).toBe(`[${WAITLIST_FIELD_CODES[code as 'invalid']}{"max":254}]`)
    })
  }

  it('says to agree for a consent field the server refused, and keeps the address', () => {
    const error = localizeWaitlistError(
      new ApiError({
        code: 'invalid_argument',
        message: 'some fields are invalid',
        status: 400,
        details: { consent: 'required', email: 'invalid' },
      }),
      t,
      fields,
    )
    expect(error.details).toEqual({
      consent: '[govue.waitlist.error.consentRequired{"max":0}]',
      email: '[govue.waitlist.error.invalid{"max":254}]',
    })
  })

  it('drops a field the form does not show', () => {
    const error = localizeWaitlistError(
      new ApiError({ code: 'invalid_argument', message: 'x', details: { name: 'required' } }),
      t,
      fields,
    )
    expect(error.details).toEqual({})
    expect(error.message).toBe('[govue.waitlist.error.invalidRequest]')
  })

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
    too_many_requests: 'rateLimited',
    request_too_large: 'tooLarge',
    unsupported_media_type: 'invalidRequest',
    method_not_allowed: 'invalidRequest',
    invalid_argument: 'invalidRequest',
    internal: 'generic',
    network: 'network',
    timeout: 'network',
  }
  for (const [code, key] of Object.entries(expected)) {
    it(`maps ${code} to ${key}`, () => {
      const error = localizeWaitlistError(new ApiError({ code, message: 'English' }), t, fields)
      expect(error.message).toBe(`[govue.waitlist.error.${key}]`)
    })
  }

  it('names the wait of a refusal that carries a Retry-After', () => {
    for (const [code, key] of Object.entries(WAITLIST_WAIT_CODES)) {
      const error = localizeWaitlistError(
        new ApiError({ code, message: 'English', retryAfter: 300 }),
        t,
        fields,
      )
      expect(error.message).toBe(`[${key}{"wait":"[govue.contact.wait.minutes{\\"n\\":5}]"}]`)
      expect(error.retryAfter).toBe(300)
    }
  })

  it('maps a 403 with a code it does not know to the refused text', () => {
    const error = localizeWaitlistError(
      new ApiError({ code: 'forbidden_by_proxy', message: 'x', status: 403 }),
      t,
      fields,
    )
    expect(error.message).toBe('[govue.waitlist.error.refused]')
  })

  it('shows its own text for a failure of the proof of work in the browser', () => {
    for (const kind of ['salt', 'worker', 'timeout', 'no_number', 'aborted'] as const) {
      const error = localizeWaitlistError(new ChallengeError(kind, 'x'), t, fields)
      expect(error.message, kind).toBe('[govue.waitlist.error.solver]')
    }
  })

  it('falls back to the generic text for an unknown code and for anything else', () => {
    expect(
      localizeWaitlistError(new ApiError({ code: 'brand_new', message: 'x' }), t, fields).message,
    ).toBe('[govue.waitlist.error.generic]')
    expect(localizeWaitlistError(new Error('boom'), t, fields).message).toBe(
      '[govue.waitlist.error.generic]',
    )
  })

  it('keeps the limit of the Go package', () => {
    expect(WAITLIST_LIMITS).toEqual({ email: 254 })
  })
})
