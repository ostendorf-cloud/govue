// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { beforeEach, describe, expect, it, vi } from 'vitest'

import { createLanguageStorage } from './languageStorage'

beforeEach(() => {
  vi.restoreAllMocks()
  document.cookie = 'lang-test=; Path=/; Max-Age=0'
})

describe('cookie storage', () => {
  it('round trips a value', () => {
    const storage = createLanguageStorage({ kind: 'cookie', name: 'lang-test' })
    expect(storage.read()).toBeUndefined()
    storage.write('de')
    expect(storage.read()).toBe('de')
  })

  it('writes Path, a long Max-Age and SameSite=Lax, and no HttpOnly', () => {
    let written = ''
    vi.spyOn(document, 'cookie', 'set').mockImplementation((value) => {
      written = value
    })
    createLanguageStorage({ kind: 'cookie', name: 'lang-test' }).write('en')
    expect(written).toContain('lang-test=en')
    expect(written).toContain('Path=/')
    expect(written).toContain('SameSite=Lax')
    expect(written).toContain('Max-Age=31536000')
    expect(written).not.toContain('HttpOnly')
    // jsdom serves http, so no Secure here.
    expect(written).not.toContain('Secure')
  })

  it('adds Secure on https', () => {
    let written = ''
    vi.stubGlobal('location', { protocol: 'https:' })
    vi.spyOn(document, 'cookie', 'set').mockImplementation((value) => {
      written = value
    })
    createLanguageStorage({ kind: 'cookie', name: 'lang-test' }).write('en')
    vi.unstubAllGlobals()
    expect(written).toContain('; Secure')
  })

  it('does not throw when the cookie jar is blocked', () => {
    vi.spyOn(document, 'cookie', 'get').mockImplementation(() => {
      throw new Error('blocked')
    })
    vi.spyOn(document, 'cookie', 'set').mockImplementation(() => {
      throw new Error('blocked')
    })
    const storage = createLanguageStorage({ kind: 'cookie' })
    expect(storage.read()).toBeUndefined()
    expect(() => storage.write('de')).not.toThrow()
  })
})

describe('cookie options', () => {
  function written(option: Parameters<typeof createLanguageStorage>[0]): string {
    let out = ''
    vi.spyOn(document, 'cookie', 'set').mockImplementation((value) => {
      out = value
    })
    createLanguageStorage(option).write('de')
    return out
  }

  it.each(['bad name', 'a;b', 'a=b', '', 'x\ny'])(
    'falls back to the default name for %j',
    (name) => {
      expect(written({ kind: 'cookie', name })).toMatch(/^govue-language=de; /)
    },
  )

  it.each([-1, 1.5, Number.NaN, Number.POSITIVE_INFINITY])(
    'falls back to the default Max-Age for %s',
    (maxAge) => {
      expect(written({ kind: 'cookie', maxAge })).toContain('Max-Age=31536000')
    },
  )

  it('accepts a valid name and Max-Age, zero included', () => {
    const out = written({ kind: 'cookie', name: 'my.lang_1', maxAge: 0 })
    expect(out).toMatch(/^my\.lang_1=de; /)
    expect(out).toContain('Max-Age=0')
  })
})

describe('none', () => {
  it('keeps nothing', () => {
    const storage = createLanguageStorage({ kind: 'none' })
    storage.write('de')
    expect(storage.read()).toBeUndefined()
  })
})
