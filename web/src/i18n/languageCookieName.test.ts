// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

import { describe, expect, it } from 'vitest'

import { LANGUAGE_STORAGE_KEY } from './languageStorage'

// The Go package language reads the cookie the cookie storage kind writes. It
// is read from the Go source, so the two names cannot drift apart.
const goSource = readFileSync(resolve(import.meta.dirname, '../../../language/language.go'), 'utf8')

describe('the language redirect cookie', () => {
  it('has the name the cookie storage writes', () => {
    const match = /const CookieName = "([^"]+)"/.exec(goSource)
    expect(match?.[1]).toBe(LANGUAGE_STORAGE_KEY)
  })
})
