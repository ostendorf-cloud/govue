import { describe, expect, it } from 'vitest'

import { ApiError, isApiError } from './errors'

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
