// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { readFileSync, readdirSync } from 'node:fs'
import { resolve } from 'node:path'

import { describe, expect, it } from 'vitest'

import {
  failures,
  NON_TEXT_MINIMUM,
  NON_TEXT_PAIRS,
  ratio,
  round,
  TEXT_MINIMUM,
  TEXT_PAIRS,
  tokens,
  type Palette,
} from './contrast'

const stylesDir = resolve(import.meta.dirname, '.')
const themesDir = resolve(stylesDir, 'themes')

/** themeFiles is every theme shipped with the package, found rather than
 *  listed: a theme added without a line here is still checked. */
function themeFiles(): string[] {
  return readdirSync(themesDir)
    .filter((name) => name.endsWith('.css'))
    .sort()
}

function read(file: string): string {
  return readFileSync(file, 'utf8')
}

describe('the ratio helper', () => {
  it('matches the known values', () => {
    expect(round(ratio('#000000', '#ffffff'))).toBe(21)
    expect(round(ratio('#ffffff', '#ffffff'))).toBe(1)
    // The classic AA boundary pair.
    expect(round(ratio('#767676', '#ffffff'))).toBe(4.54)
  })
})

describe('the check itself fails a bad palette', () => {
  // The proof that this file can fail. Mid grey on white is 3.54:1: enough for
  // a boundary, not for text, which is exactly the mistake a palette makes.
  const bad: Palette = { text: '#949494', bg: '#ffffff', border: '#f0f0f0', surface: '#ffffff' }

  it('rejects text below 4.5:1', () => {
    expect(failures(bad, [['text', 'bg']], TEXT_MINIMUM)).toEqual([
      'text on bg is 3.03:1, want 4.5:1',
    ])
  })

  it('rejects a boundary below 3:1', () => {
    expect(failures(bad, [['border', 'surface']], NON_TEXT_MINIMUM)).toEqual([
      'border on surface is 1.14:1, want 3:1',
    ])
  })

  it('passes the same pair once it is dark enough', () => {
    expect(failures({ text: '#595959', bg: '#ffffff' }, [['text', 'bg']], TEXT_MINIMUM)).toEqual([])
  })

  it('names the token a palette is missing instead of throwing', () => {
    expect(failures({ bg: '#ffffff' }, [['text', 'bg']], TEXT_MINIMUM)).toEqual([
      'text on bg cannot be checked, text is not defined',
    ])
    expect(failures({}, [['text', 'bg']], TEXT_MINIMUM)).toEqual([
      'text on bg cannot be checked, text and bg is not defined',
    ])
  })
})

describe('the themes directory', () => {
  it('holds at least one theme to check', () => {
    expect(themeFiles().length).toBeGreaterThan(0)
  })
})

describe.each([
  ['the default palette', resolve(stylesDir, 'base.css')],
  ...themeFiles().map((name) => [name.replace(/\.css$/, ''), resolve(themesDir, name)]),
])('%s', (_name, file) => {
  const palettes = tokens(read(file))

  it('defines every token the components use', () => {
    const required = new Set([...TEXT_PAIRS, ...NON_TEXT_PAIRS].flat())
    for (const mode of ['light', 'dark'] as const) {
      for (const token of required) {
        expect(palettes[mode], `--color-${token} in ${mode}`).toHaveProperty(token)
      }
    }
  })

  it.each(['light', 'dark'] as const)('reaches AA for text in %s mode', (mode) => {
    expect(failures(palettes[mode], TEXT_PAIRS, TEXT_MINIMUM)).toEqual([])
  })

  it.each(['light', 'dark'] as const)('reaches AA for boundaries in %s mode', (mode) => {
    expect(failures(palettes[mode], NON_TEXT_PAIRS, NON_TEXT_MINIMUM)).toEqual([])
  })
})
