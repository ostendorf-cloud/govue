// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

/**
 * Where the bearer client keeps the device token of a native app.
 *
 * The client stores ONE opaque string value. Today it is a small JSON record
 * with a version, the token and the time it was issued or last rotated, and an
 * adapter must store and return it unchanged. An adapter never parses it, never
 * logs it and never copies it anywhere else. A product supplies an adapter that
 * is backed by the platform keychain. govue ships the in memory adapter only.
 *
 * `get` answers null or undefined when nothing is stored. A method that throws
 * or rejects makes the client fail with an ApiError of code `token_storage`
 * and send nothing.
 */
export interface TokenStorage {
  get(): Promise<string | null | undefined>
  set(value: string): Promise<void>
  clear(): Promise<void>
}

/**
 * createMemoryTokenStorage is the default adapter. The value lives as long as
 * the page or the app process does, and nothing else can read it.
 */
export function createMemoryTokenStorage(): TokenStorage {
  let value: string | null = null
  return {
    get: () => Promise.resolve(value),
    set: (next) => {
      value = next
      return Promise.resolve()
    },
    clear: () => {
      value = null
      return Promise.resolve()
    },
  }
}
