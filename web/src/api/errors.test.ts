// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'

import { ApiError, isApiError, parseRetryAfter } from './errors'

describe('isApiError', () => {
  it('narrows an ApiError instance', () => {
    const error: unknown = new ApiError({ code: 'not_found', message: 'thing not found' })

    expect(isApiError(error)).toBe(true)
  })

  it('rejects a plain object shaped like one', () => {
    const notAnError: unknown = { code: 'not_found', message: 'thing not found' }

    expect(isApiError(notAnError)).toBe(false)
  })

  it('rejects a plain Error', () => {
    expect(isApiError(new Error('boom'))).toBe(false)
  })
})

describe('ApiError retryAfter', () => {
  it('keeps a whole number of seconds within a day', () => {
    for (const seconds of [1, 120, 86_400]) {
      expect(new ApiError({ code: 'x', message: 'y', retryAfter: seconds }).retryAfter).toBe(
        seconds,
      )
    }
  })

  it('drops anything else, and is undefined by default', () => {
    expect(new ApiError({ code: 'x', message: 'y' }).retryAfter).toBeUndefined()
    for (const seconds of [0, -1, 1.5, Number.NaN, Number.POSITIVE_INFINITY, 86_401]) {
      expect(
        new ApiError({ code: 'x', message: 'y', retryAfter: seconds }).retryAfter,
      ).toBeUndefined()
    }
  })
})

describe('parseRetryAfter', () => {
  it('reads digits only', () => {
    expect(parseRetryAfter('2')).toBe(2)
    expect(parseRetryAfter(' 300 ')).toBe(300)
    for (const value of [
      null,
      '',
      'soon',
      '-5',
      '+5',
      '1.5',
      '1e3',
      '0x10',
      'Wed, 21 Oct 2026 07:28:00 GMT',
      '86401',
      '99999999',
    ]) {
      expect(parseRetryAfter(value), String(value)).toBeUndefined()
    }
  })
})

describe('ApiError details', () => {
  it('keeps boolean values alongside strings and numbers', () => {
    const error = new ApiError({
      code: 'invalid_argument',
      message: 'bad request',
      details: { archived: true, count: 2, title: 'lamp', nested: { a: 1 } },
    })

    expect(error.details).toEqual({ archived: 'true', count: '2', title: 'lamp' })
  })
})
