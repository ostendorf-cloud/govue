// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'

import { hexToOklch, hexToRgb, maxChromaInGamut, oklchToHex } from './color.ts'

// Sequential digit placeholders, picked specifically to be unmistakably
// fabricated for this test file rather than resembling any real brand's
// colour. None of these appear in base.css, sample.css or sample-contrast.css.
const ARBITRARY_HEXES = ['#123456', '#654321', '#4f6b2a', '#2a4f6b']

function channelDelta(a: string, b: string): number {
  const ra = hexToRgb(a)
  const rb = hexToRgb(b)
  return Math.max(Math.abs(ra.r - rb.r), Math.abs(ra.g - rb.g), Math.abs(ra.b - rb.b))
}

describe('hexToOklch / oklchToHex round trip', () => {
  it.each(ARBITRARY_HEXES)('stays close to %s', (hex) => {
    const oklch = hexToOklch(hex)
    const roundTripped = oklchToHex(oklch)
    // The original chroma is always in gamut for its own lightness and hue
    // (it came from a real sRGB colour), so the round trip should not need to
    // clamp anything away; a couple of sRGB levels of float/rounding error is
    // the only slack allowed.
    expect(channelDelta(hex, roundTripped)).toBeLessThanOrEqual(2)
  })
})

describe('boundary values', () => {
  it('keeps pure black achromatic and black', () => {
    const oklch = hexToOklch('#000000')
    expect(oklch.l).toBeCloseTo(0, 5)
    expect(oklch.c).toBeCloseTo(0, 5)
    expect(oklchToHex(oklch)).toBe('#000000')
  })

  it('keeps pure white achromatic and white', () => {
    const oklch = hexToOklch('#ffffff')
    expect(oklch.l).toBeCloseTo(1, 4)
    expect(oklch.c).toBeCloseTo(0, 4)
    expect(oklchToHex(oklch)).toBe('#ffffff')
  })

  it('keeps a pure grey achromatic', () => {
    const oklch = hexToOklch('#808080')
    expect(oklch.c).toBeCloseTo(0, 3)
    expect(channelDelta('#808080', oklchToHex(oklch))).toBeLessThanOrEqual(1)
  })
})

describe('gamut clamp', () => {
  it('never returns a chroma that pushes a channel out of range', () => {
    // A deliberately absurd request: far more chroma than any hue can carry
    // at these lightnesses. The clamp inside oklchToHex has to bring it back
    // into a real colour rather than let a channel wrap or clip silently.
    for (const l of [0.02, 0.1, 0.5, 0.9, 0.98]) {
      for (const h of [0, 90, 180, 270, 320]) {
        const hex = oklchToHex({ l, c: 5, h })
        expect(hex).toMatch(/^#[0-9a-f]{6}$/)
        const { r, g, b } = hexToRgb(hex)
        for (const channel of [r, g, b]) {
          expect(channel).toBeGreaterThanOrEqual(0)
          expect(channel).toBeLessThanOrEqual(255)
        }
      }
    }
  })

  it('reports less chroma available near the lightness extremes than in the middle', () => {
    // The gamut narrows to a point at L=0 and L=1: a saturated colour has to
    // sit somewhere in between to exist at all in sRGB.
    const atExtreme = maxChromaInGamut(0.02, 280)
    const inMiddle = maxChromaInGamut(0.6, 280)
    expect(atExtreme).toBeLessThan(inMiddle)
  })

  it('finds zero chroma available outside the lightness range', () => {
    expect(maxChromaInGamut(-0.5, 280)).toBe(0)
    expect(maxChromaInGamut(1.5, 280)).toBe(0)
  })
})
