// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'

import {
  failures,
  NON_TEXT_MINIMUM,
  NON_TEXT_PAIRS,
  TEXT_MINIMUM,
  TEXT_PAIRS,
  tokens,
} from '../styles/contrast.ts'
import { generateTheme } from './generate.ts'

// Fictional seeds, distinct from every other test file's: a moss green and
// a cobalt blue, neither resembling a real brand.
const VALID_CONFIG = { name: 'fixture-generate', seeds: { neutral: '#3e5c3a', accent: '#2d5f9e' } }

describe('generateTheme end to end', () => {
  it('produces CSS that passes the same WCAG check contrast.test.ts runs on shipped themes', () => {
    const result = generateTheme(VALID_CONFIG)
    expect(result.ok).toBe(true)
    if (!result.ok) {
      return
    }
    const palettes = tokens(result.css)
    expect(failures(palettes.light, TEXT_PAIRS, TEXT_MINIMUM)).toEqual([])
    expect(failures(palettes.light, NON_TEXT_PAIRS, NON_TEXT_MINIMUM)).toEqual([])
    expect(failures(palettes.dark, TEXT_PAIRS, TEXT_MINIMUM)).toEqual([])
    expect(failures(palettes.dark, NON_TEXT_PAIRS, NON_TEXT_MINIMUM)).toEqual([])
  })

  it('selects the configured name', () => {
    const result = generateTheme(VALID_CONFIG)
    expect(result.ok).toBe(true)
    if (result.ok) {
      expect(result.css).toContain(`:root[data-theme='${VALID_CONFIG.name}']`)
    }
  })

  it('rejects an invalid config before ever touching the palette solver', () => {
    const result = generateTheme({ name: 'fixture', seeds: { neutral: 'not-a-colour' } })
    expect(result.ok).toBe(false)
    if (!result.ok) {
      expect(result.errors.length).toBeGreaterThan(0)
      expect(result.errors.some((message) => message.includes('seeds.'))).toBe(true)
    }
  })

  it('rejects a config that is not an object', () => {
    const result = generateTheme('not a config')
    expect(result.ok).toBe(false)
  })
})
