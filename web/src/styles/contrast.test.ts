import { readFileSync, readdirSync } from 'node:fs'
import { resolve } from 'node:path'

import { describe, expect, it } from 'vitest'

// The pairs that have to hold in every theme, in both modes. A theme is only
// finished when this passes: a palette that looks right and fails here is not
// usable, and checking it by eye does not work.
//
// 4.5 is WCAG 2.2 AA for body text, 3 for a boundary, an icon or a focus ring.
const TEXT_MINIMUM = 4.5
const NON_TEXT_MINIMUM = 3

const TEXT_PAIRS: Array<[fg: string, bg: string]> = [
  ['text', 'bg'],
  ['text', 'bg-subtle'],
  ['text', 'surface'],
  ['text', 'surface-hover'],
  ['text-muted', 'bg'],
  ['text-muted', 'surface'],
  // Hints and placeholders are text too, however quiet they look.
  ['text-faint', 'bg'],
  ['text-faint', 'surface'],
  ['accent-text', 'bg'],
  ['accent-text', 'surface'],
  ['accent-text', 'accent-subtle'],
  ['accent-on', 'accent-strong'],
  // The hover state of a solid control has to hold the same contrast as its
  // resting state; a hover that only looks different is not enough.
  ['accent-on', 'accent-strong-hover'],
  ['danger-on', 'danger-strong'],
  ['danger-on', 'danger-strong-hover'],
  ['success-text', 'surface'],
  ['success-text', 'success-subtle'],
  ['warning-text', 'surface'],
  ['warning-text', 'warning-subtle'],
  ['danger-text', 'surface'],
  ['danger-text', 'danger-subtle'],
  ['info-text', 'surface'],
  ['info-text', 'info-subtle'],
]

const NON_TEXT_PAIRS: Array<[fg: string, bg: string]> = [
  // A control's own outline has to be visible against what is behind it.
  ['border-strong', 'bg'],
  ['border-strong', 'surface'],
  // Fills and icons.
  ['accent', 'bg'],
  ['accent', 'surface'],
  // The focus ring, on every surface it can appear over.
  ['focus', 'bg'],
  ['focus', 'surface'],
  ['focus', 'surface-hover'],
]

const stylesDir = resolve(import.meta.dirname, '.')
const themesDir = resolve(stylesDir, 'themes')

type Palette = Record<string, string>

/** tokens reads the hex token pairs of one stylesheet, one map per mode. */
function tokens(css: string): { light: Palette; dark: Palette } {
  const light: Palette = {}
  const dark: Palette = {}
  // Only the hex pairs: an overlay or a shadow carries alpha and is not part of
  // a contrast requirement.
  const pattern =
    /--color-([\w-]+):\s*light-dark\(\s*(#[0-9a-fA-F]{6})\s*,\s*(#[0-9a-fA-F]{6})\s*\)/g
  for (const match of css.matchAll(pattern)) {
    const [, name, lightValue, darkValue] = match
    light[name] = lightValue
    dark[name] = darkValue
  }
  return { light, dark }
}

/** channel is one sRGB channel, linearised as WCAG 2.2 defines it. */
function channel(value: number): number {
  const srgb = value / 255
  return srgb <= 0.03928 ? srgb / 12.92 : ((srgb + 0.055) / 1.055) ** 2.4
}

function luminance(hex: string): number {
  const value = Number.parseInt(hex.slice(1), 16)
  const r = channel((value >> 16) & 0xff)
  const g = channel((value >> 8) & 0xff)
  const b = channel(value & 0xff)
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}

function ratio(foreground: string, background: string): number {
  const a = luminance(foreground)
  const b = luminance(background)
  const [lighter, darker] = a > b ? [a, b] : [b, a]
  return (lighter + 0.05) / (darker + 0.05)
}

/** round keeps a failure message readable. */
function round(value: number): number {
  return Math.round(value * 100) / 100
}

/** failures lists the pairs of one palette that stay under the minimum. */
function failures(
  palette: Palette,
  pairs: Array<[fg: string, bg: string]>,
  minimum: number,
): string[] {
  return pairs
    .filter(([fg, bg]) => ratio(palette[fg], palette[bg]) < minimum)
    .map(
      ([fg, bg]) =>
        `${fg} on ${bg} is ${round(ratio(palette[fg], palette[bg]))}:1, want ${minimum}:1`,
    )
}

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
