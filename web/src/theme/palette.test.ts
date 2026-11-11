import { describe, expect, it } from 'vitest'

import {
  failures,
  NON_TEXT_MINIMUM,
  NON_TEXT_PAIRS,
  TEXT_MINIMUM,
  TEXT_PAIRS,
} from '../styles/contrast.ts'
import { hexToOklch, oklchToHex } from './color.ts'
import type { ThemeSeedConfig } from './config.ts'
import { buildPalette } from './palette.ts'

// Fictional, round seeds distinct from base.css, sample.css,
// sample-contrast.css, color.test.ts and config.test.ts: a slate blue and a
// burnt orange, neither resembling a real brand's palette.
const REASONABLE_CONFIG: ThemeSeedConfig = {
  name: 'fixture',
  seeds: { neutral: '#38506b', accent: '#b5651d' },
}

describe('buildPalette with a reasonable seed pair', () => {
  const result = buildPalette(REASONABLE_CONFIG)

  it('succeeds', () => {
    expect(result.ok).toBe(true)
  })

  it('clears every required pair in both modes, verified through failures()', () => {
    if (!result.ok) {
      throw new Error('expected buildPalette to succeed')
    }
    expect(failures(result.light, TEXT_PAIRS, TEXT_MINIMUM)).toEqual([])
    expect(failures(result.light, NON_TEXT_PAIRS, NON_TEXT_MINIMUM)).toEqual([])
    expect(failures(result.dark, TEXT_PAIRS, TEXT_MINIMUM)).toEqual([])
    expect(failures(result.dark, NON_TEXT_PAIRS, NON_TEXT_MINIMUM)).toEqual([])
  })

  it('declares every hex role base.css needs for the pair checks', () => {
    if (!result.ok) {
      throw new Error('expected buildPalette to succeed')
    }
    const required = new Set([...TEXT_PAIRS, ...NON_TEXT_PAIRS].flat())
    for (const token of required) {
      expect(result.light, token).toHaveProperty(token)
      expect(result.dark, token).toHaveProperty(token)
    }
  })

  it('carries no warnings for an unremarkable seed pair', () => {
    if (!result.ok) {
      throw new Error('expected buildPalette to succeed')
    }
    expect(result.warnings).toEqual([])
  })
})

describe('buildPalette with an accent seed too close in hue to the neutral seed', () => {
  // Same hue and chroma as the neutral seed, only lighter, so the accent is
  // numerically a different colour but sits on the same hue.
  const neutralOklch = hexToOklch(REASONABLE_CONFIG.seeds.neutral)
  const closeAccent = oklchToHex({
    l: Math.min(0.95, neutralOklch.l + 0.2),
    c: neutralOklch.c,
    h: neutralOklch.h,
  })
  const config: ThemeSeedConfig = {
    name: 'fixture-close',
    seeds: { neutral: REASONABLE_CONFIG.seeds.neutral, accent: closeAccent },
  }
  const result = buildPalette(config)

  it('still passes WCAG', () => {
    expect(result.ok).toBe(true)
    if (result.ok) {
      expect(failures(result.light, TEXT_PAIRS, TEXT_MINIMUM)).toEqual([])
      expect(failures(result.dark, TEXT_PAIRS, TEXT_MINIMUM)).toEqual([])
    }
  })

  it('carries the "not visually distinct" warning', () => {
    expect(result.ok).toBe(true)
    if (result.ok) {
      expect(
        result.warnings.some((message) => message.includes('not read as visually distinct')),
      ).toBe(true)
    }
  })
})

describe('whether a seed pair can fail WCAG by construction', () => {
  // The solver always has room to move a role's lightness all the way to 0
  // or 1, where the WCAG ratio against any realistic background approaches
  // its 21:1 maximum, so the bisection in solve() always finds a passing
  // lightness for any anchor that is not itself at the opposite extreme.
  // There is no seed pair this design can hand the solver that produces an
  // unsolvable pair; only the defensive re-verification in buildPalette can
  // ever return ok: false, and that is a regression guard, not a path a
  // valid hex seed can reach. This case documents that conclusion instead of
  // forcing a fake failure: even a seed pair built from the two extremes,
  // pure black and pure white, still clears every pair.
  const extremeConfig: ThemeSeedConfig = {
    name: 'fixture-extreme',
    seeds: { neutral: '#000000', accent: '#ffffff' },
  }

  it('still succeeds', () => {
    const result = buildPalette(extremeConfig)
    expect(result.ok).toBe(true)
  })
})
