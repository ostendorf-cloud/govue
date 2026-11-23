// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { readFileSync, readdirSync } from 'node:fs'
import { resolve } from 'node:path'

import { describe, expect, it } from 'vitest'

import {
  composite,
  DARKEST_CONTENT,
  failures,
  HEADER_NON_TEXT_PAIRS,
  HEADER_TEXT_PAIRS,
  LIGHTEST_CONTENT,
  luminance,
  maxBackdropLuminance,
  NON_TEXT_MINIMUM,
  NON_TEXT_PAIRS,
  ratio,
  round,
  TRANSLUCENT_ALPHA_ALL_TEXT,
  TRANSLUCENT_ALPHA_TEXT,
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

  // The header ground, default and translucent, in every shipped theme.
  it('defines the header ground in both modes', () => {
    expect(palettes.light).toHaveProperty('header-bg')
    expect(palettes.dark).toHaveProperty('header-bg')
  })

  it.each(['light', 'dark'] as const)('reaches AA on the header ground in %s mode', (mode) => {
    expect(failures(palettes[mode], HEADER_TEXT_PAIRS, TEXT_MINIMUM)).toEqual([])
    expect(failures(palettes[mode], HEADER_NON_TEXT_PAIRS, NON_TEXT_MINIMUM)).toEqual([])
  })

  // A translucent ground over the lightest and the darkest page content. The
  // header-bg alpha documented in the concept doc is what this proves.
  describe.each(['light', 'dark'] as const)('a translucent header ground in %s mode', (mode) => {
    const palette = palettes[mode]

    it.each([LIGHTEST_CONTENT, DARKEST_CONTENT])(
      'keeps text at 4.5:1 at the documented alpha over %s',
      (content) => {
        const ground = composite(palette['header-bg'], TRANSLUCENT_ALPHA_TEXT, content)
        expect(failures({ ...palette, ground }, [['text', 'ground']], TEXT_MINIMUM)).toEqual([])
      },
    )

    it.each([LIGHTEST_CONTENT, DARKEST_CONTENT])(
      'keeps every header text role at 4.5:1 at the strict documented alpha over %s',
      (content) => {
        const ground = composite(palette['header-bg'], TRANSLUCENT_ALPHA_ALL_TEXT, content)
        const pairs = HEADER_TEXT_PAIRS.map(([fg]): [string, string] => [fg, 'ground'])
        expect(failures({ ...palette, ground }, pairs, TEXT_MINIMUM)).toEqual([])
      },
    )

    it('is opaque over its own surface, so a solid ground is the alpha 1 case', () => {
      expect(composite(palette['header-bg'], 1, DARKEST_CONTENT)).toBe(palette['header-bg'])
    })
  })

  // The transparent overlay state shows the dark-mode roles whatever the page
  // mode is. Dark text has to hold on the darkest ground the host may put
  // under the header: its luminance bound is what the docs state.
  it('keeps the transparent overlay state legible on the dark hero it promises', () => {
    const dark = palettes.dark
    const bound = maxBackdropLuminance(dark.text)
    expect(luminance(dark.bg)).toBeLessThanOrEqual(bound)
    expect(luminance(DARKEST_CONTENT)).toBeLessThanOrEqual(bound)
    expect(luminance(LIGHTEST_CONTENT)).toBeGreaterThan(bound)
    expect(
      failures(
        dark,
        [
          ['text', 'bg'],
          ['text-muted', 'bg'],
          ['text-faint', 'bg'],
          ['accent-text', 'bg'],
        ],
        TEXT_MINIMUM,
      ),
    ).toEqual([])
    expect(failures(dark, [['focus', 'bg']], NON_TEXT_MINIMUM)).toEqual([])
  })
})

describe('the translucent header check itself fails', () => {
  const palette = tokens(read(resolve(stylesDir, 'base.css')))

  it('rejects a ground too transparent for the text', () => {
    // Half alpha of the light surface over black is 3.54:1 for the light text.
    const ground = composite(palette.light['header-bg'], 0.5, DARKEST_CONTENT)
    expect(failures({ ...palette.light, ground }, [['text', 'ground']], TEXT_MINIMUM)).not.toEqual(
      [],
    )
  })

  it('rejects the dark mode ground at an alpha under the documented one', () => {
    const ground = composite(palette.dark['header-bg'], 0.75, LIGHTEST_CONTENT)
    expect(failures({ ...palette.dark, ground }, [['text', 'ground']], TEXT_MINIMUM)).not.toEqual(
      [],
    )
  })
})
