// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

/** The localStorage key and the cookie name the choice is kept under. */
export const LANGUAGE_STORAGE_KEY = 'govue-language'

/** One year, in seconds. A language is not a session setting. */
const COOKIE_MAX_AGE = 60 * 60 * 24 * 365

/** Letters, digits, dot, underscore and hyphen: no ;, =, space or control character. */
const COOKIE_NAME = /^[A-Za-z0-9._-]+$/

/**
 * Where the visitor's choice lives.
 *
 * local is the default: a localStorage key. cookie is for signed in app
 * areas whose server reads the language too. It is a functional preference
 * cookie: it holds a language code, nothing identifies a person, and it is set
 * only when the visitor picks a language. custom hands the read and the write
 * to the app, for a value kept on the account. none keeps nothing.
 *
 * A cookie name with a character outside letters, digits, dot, underscore and
 * hyphen, and a maxAge that is not a non-negative integer, are ignored: the
 * defaults apply.
 */
export type LanguageStorageOption =
  | { kind: 'local'; key?: string }
  | { kind: 'cookie'; name?: string; maxAge?: number }
  | {
      kind: 'custom'
      /** Synchronous, because the first render needs the language. */
      get: () => string | null | undefined
      set: (code: string) => void
    }
  | { kind: 'none' }

export interface LanguageStorage {
  read: () => string | undefined
  write: (code: string) => void
}

/**
 * createLanguageStorage never throws. Blocked storage, a private window and
 * a failing callback all mean "nothing stored" on read and "the choice lasts
 * for this page" on write.
 */
export function createLanguageStorage(
  option: LanguageStorageOption = { kind: 'local' },
): LanguageStorage {
  switch (option.kind) {
    case 'local': {
      const key = option.key ?? LANGUAGE_STORAGE_KEY
      return {
        read: () => attempt(() => globalThis.localStorage?.getItem(key) ?? undefined),
        write: (code) => {
          attempt(() => globalThis.localStorage?.setItem(key, code))
        },
      }
    }
    case 'cookie': {
      // An invalid name or max age would write a different cookie than the
      // app meant, or inject an attribute, so each falls back to its default.
      const name =
        option.name !== undefined && COOKIE_NAME.test(option.name)
          ? option.name
          : LANGUAGE_STORAGE_KEY
      const maxAge =
        option.maxAge !== undefined && Number.isInteger(option.maxAge) && option.maxAge >= 0
          ? option.maxAge
          : COOKIE_MAX_AGE
      return {
        read: () => attempt(() => readCookie(name)),
        write: (code) => {
          attempt(() => {
            if (typeof document === 'undefined') {
              return
            }
            // Lax and Path=/ always, Secure on https, never HttpOnly: the
            // client has to read it. The code is one of our own locale codes,
            // and encodeURIComponent keeps the value safe either way.
            const secure = globalThis.location?.protocol === 'https:' ? '; Secure' : ''
            document.cookie = `${name}=${encodeURIComponent(code)}; Path=/; Max-Age=${maxAge}; SameSite=Lax${secure}`
          })
        },
      }
    }
    case 'custom':
      return {
        read: () => attempt(() => option.get() ?? undefined),
        write: (code) => {
          attempt(() => option.set(code))
        },
      }
    case 'none':
      return { read: () => undefined, write: () => {} }
  }
}

function readCookie(name: string): string | undefined {
  if (typeof document === 'undefined') {
    return undefined
  }
  for (const part of document.cookie.split(';')) {
    const index = part.indexOf('=')
    if (index !== -1 && part.slice(0, index).trim() === name) {
      return decodeURIComponent(part.slice(index + 1).trim())
    }
  }
  return undefined
}

function attempt<T>(fn: () => T): T | undefined {
  try {
    return fn()
  } catch {
    return undefined
  }
}
