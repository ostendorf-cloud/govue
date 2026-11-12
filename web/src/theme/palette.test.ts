// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

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
import { buildPalette, collectFailures, type HexPalette } from './palette.ts'

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
  // valid hex seed can reach (see collectFailures's own comment in
  // palette.ts). This case documents that conclusion instead of forcing a
  // fake failure: even a seed pair built from the two extremes, pure black
  // and pure white, still clears every pair. The branch itself still gets
  // real coverage below, through collectFailures directly.
  const extremeConfig: ThemeSeedConfig = {
    name: 'fixture-extreme',
    seeds: { neutral: '#000000', accent: '#ffffff' },
  }

  it('still succeeds', () => {
    const result = buildPalette(extremeConfig)
    expect(result.ok).toBe(true)
  })
})

describe('collectFailures, the check behind the ok: false branch in buildPalette', () => {
  // No valid seed reaches this branch (see the test above and collectFailures's
  // comment), so it is exercised here directly: a synthetic palette, built by
  // hand rather than solved, that is known to fail WCAG by construction. This
  // is the error-aggregation and formatting logic itself, independent of
  // whether solve() could ever hand it a failing result.
  const passing: HexPalette = {
    bg: '#ffffff',
    'bg-subtle': '#f2f2f2',
    surface: '#ffffff',
    'surface-hover': '#eaeaea',
    border: '#cccccc',
    'border-strong': '#595959',
    text: '#1a1a1a',
    'text-muted': '#333333',
    'text-faint': '#4d4d4d',
    accent: '#0b5fff',
    'accent-text': '#0b3f99',
    'accent-strong': '#0b3f99',
    'accent-strong-hover': '#082c6b',
    'accent-on': '#ffffff',
    'accent-subtle': '#dce7ff',
    focus: '#0b5fff',
    'success-text': '#1a1a1a',
    'success-subtle': '#dce7ff',
    'warning-text': '#1a1a1a',
    'warning-subtle': '#dce7ff',
    'danger-text': '#1a1a1a',
    'danger-subtle': '#dce7ff',
    'danger-strong': '#0b3f99',
    'danger-strong-hover': '#082c6b',
    'danger-on': '#ffffff',
    'info-text': '#1a1a1a',
    'info-subtle': '#dce7ff',
  }

  it('reports nothing for a palette that genuinely clears every pair', () => {
    expect(collectFailures(passing, passing)).toEqual([])
  })

  it('reports every failing pair, prefixed by mode, for a palette that cannot', () => {
    // Every text and non-text role collapsed onto its own background's
    // colour: every required pair is 1:1, which clears neither WCAG minimum.
    const failing: HexPalette = Object.fromEntries(
      Object.keys(passing).map((role) => [role, '#808080']),
    )

    const errors = collectFailures(failing, failing)

    expect(errors.length).toBeGreaterThan(0)
    expect(
      errors.every(
        (message) => message.startsWith('light mode, ') || message.startsWith('dark mode, '),
      ),
    ).toBe(true)
    expect(errors.some((message) => message.includes('text on bg is 1:1, want 4.5:1'))).toBe(true)
    expect(errors.filter((message) => message.startsWith('light mode, '))).toHaveLength(
      errors.filter((message) => message.startsWith('dark mode, ')).length,
    )
  })

  it('names the missing token rather than throwing when a role is absent', () => {
    const incomplete: HexPalette = { bg: '#ffffff' }
    const errors = collectFailures(incomplete, incomplete)
    expect(errors.some((message) => message.includes('text is not defined'))).toBe(true)
  })
})
