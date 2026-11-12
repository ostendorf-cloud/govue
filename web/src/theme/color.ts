// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// OKLCH is the working space for the theme generator, not HSL. OKLCH's
// lightness axis is close to perceptually uniform and correlates predictably
// with relative luminance: moving a role's L by a fixed amount gives a
// roughly fixed change in WCAG contrast ratio regardless of hue. HSL's L does
// not have that property, identical HSL lightness at different hues can sit
// at wildly different actual luminance, which makes solving for a target
// ratio by search unreliable in HSL and reliable in OKLCH. The WCAG ratio
// itself is still computed by the exact formula in ../styles/contrast.ts;
// OKLCH only decides how the generator searches for a colour, never what
// counts as passing.
//
// The conversion matrices below are Björn Ottosson's published OKLab
// reference (https://bottosson.github.io/posts/oklab/): sRGB <-> linear sRGB
// <-> LMS <-> OKLab, with OKLCH as OKLab in polar coordinates.

export interface Oklch {
  l: number
  c: number
  h: number
}

interface Rgb {
  r: number
  g: number
  b: number
}

function clamp01(value: number): number {
  return Math.min(1, Math.max(0, value))
}

/** hexToRgb reads a #rrggbb string into 0-255 channels. */
export function hexToRgb(hex: string): Rgb {
  const value = Number.parseInt(hex.slice(1), 16)
  return {
    r: (value >> 16) & 0xff,
    g: (value >> 8) & 0xff,
    b: value & 0xff,
  }
}

/** rgbToHex rounds and clamps three 0-255 channels into a #rrggbb string. */
export function rgbToHex({ r, g, b }: Rgb): string {
  const channel = (value: number): string =>
    Math.round(clamp01(value / 255) * 255)
      .toString(16)
      .padStart(2, '0')
  return `#${channel(r)}${channel(g)}${channel(b)}`
}

// The standard sRGB electro-optical transfer function, not WCAG's own
// slightly different threshold (0.03928 there vs 0.04045 here): this one
// round-trips sRGB <-> linear for colour conversion, WCAG's is only for the
// contrast formula in ../styles/contrast.ts.
function srgbToLinear(value: number): number {
  const c = value / 255
  return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
}

function linearToSrgb(value: number): number {
  const c = clamp01(value)
  return c <= 0.0031308 ? c * 12.92 : 1.055 * c ** (1 / 2.4) - 0.055
}

interface Linear {
  r: number
  g: number
  b: number
}

function rgbToLinear(rgb: Rgb): Linear {
  return { r: srgbToLinear(rgb.r), g: srgbToLinear(rgb.g), b: srgbToLinear(rgb.b) }
}

function linearToRgb255(linear: Linear): Rgb {
  return {
    r: linearToSrgb(linear.r) * 255,
    g: linearToSrgb(linear.g) * 255,
    b: linearToSrgb(linear.b) * 255,
  }
}

interface Lab {
  l: number
  a: number
  b: number
}

function linearToOklab(linear: Linear): Lab {
  const l = 0.4122214708 * linear.r + 0.5363325363 * linear.g + 0.0514459929 * linear.b
  const m = 0.2119034982 * linear.r + 0.6806995451 * linear.g + 0.1073969566 * linear.b
  const s = 0.0883024619 * linear.r + 0.2817188376 * linear.g + 0.6299787005 * linear.b

  const l_ = Math.cbrt(l)
  const m_ = Math.cbrt(m)
  const s_ = Math.cbrt(s)

  return {
    l: 0.2104542553 * l_ + 0.793617785 * m_ - 0.0040720468 * s_,
    a: 1.9779984951 * l_ - 2.428592205 * m_ + 0.4505937099 * s_,
    b: 0.0259040371 * l_ + 0.7827717662 * m_ - 0.808675766 * s_,
  }
}

function oklabToLinear(lab: Lab): Linear {
  const l_ = lab.l + 0.3963377774 * lab.a + 0.2158037573 * lab.b
  const m_ = lab.l - 0.1055613458 * lab.a - 0.0638541728 * lab.b
  const s_ = lab.l - 0.0894841775 * lab.a - 1.291485548 * lab.b

  const l = l_ ** 3
  const m = m_ ** 3
  const s = s_ ** 3

  return {
    r: 4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s,
    g: -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s,
    b: -0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s,
  }
}

function labToLch(lab: Lab): Oklch {
  const c = Math.sqrt(lab.a ** 2 + lab.b ** 2)
  // atan2(0, 0) is 0, so a fully achromatic colour gets hue 0 rather than NaN.
  const h = (Math.atan2(lab.b, lab.a) * 180) / Math.PI
  return { l: lab.l, c, h: h < 0 ? h + 360 : h }
}

function lchToLab(lch: Oklch): Lab {
  const radians = (lch.h * Math.PI) / 180
  return { l: lch.l, a: lch.c * Math.cos(radians), b: lch.c * Math.sin(radians) }
}

/** hexToOklch converts a #rrggbb colour into OKLCH coordinates. */
export function hexToOklch(hex: string): Oklch {
  return labToLch(linearToOklab(rgbToLinear(hexToRgb(hex))))
}

/** oklchToLinear is the unclamped linear-sRGB of one OKLCH coordinate, before
 *  gamma encoding or rounding. A gamut check reads these values directly: a
 *  channel outside [0, 1] here means the colour does not exist in sRGB, and
 *  clamping it afterwards would silently drift the hue. */
function oklchToLinear(lch: Oklch): Linear {
  return oklabToLinear(lchToLab(lch))
}

/** inGamut reports whether an OKLCH coordinate's unclamped linear-sRGB stays
 *  inside [0, 1] on every channel, with a small epsilon for float error at
 *  the boundary. */
function inGamut(lch: Oklch): boolean {
  const linear = oklchToLinear(lch)
  const epsilon = 1e-7
  return (
    linear.r >= -epsilon &&
    linear.r <= 1 + epsilon &&
    linear.g >= -epsilon &&
    linear.g <= 1 + epsilon &&
    linear.b >= -epsilon &&
    linear.b <= 1 + epsilon
  )
}

const CHROMA_SEARCH_ITERATIONS = 40
// A generous upper bound: no real sRGB colour reaches this OKLCH chroma, so
// the search always has a failing upper end to bisect against.
const MAX_SEARCHABLE_CHROMA = 0.5

/**
 * maxChromaInGamut finds, by binary search, the largest chroma at a given
 * lightness and hue whose linear-sRGB stays in [0, 1] on every channel. The
 * chroma-to-gamut-edge mapping is monotonic for a fixed L and H (moving away
 * from the achromatic point only ever leaves the cube, never re-enters it),
 * which is what makes plain bisection reliable here.
 */
export function maxChromaInGamut(l: number, h: number): number {
  if (!inGamut({ l, c: 0, h })) {
    // L itself is out of [0, 1] far enough that even grey does not fit;
    // nothing is achievable, so the edge is chroma 0.
    return 0
  }
  let low = 0
  let high = MAX_SEARCHABLE_CHROMA
  for (let i = 0; i < CHROMA_SEARCH_ITERATIONS; i++) {
    const mid = (low + high) / 2
    if (inGamut({ l, c: mid, h })) {
      low = mid
    } else {
      high = mid
    }
  }
  return low
}

/**
 * oklchToHex renders an OKLCH coordinate as a #rrggbb string. The requested
 * chroma is clamped to the gamut edge at this lightness and hue first, so the
 * result never silently clips a channel and drifts hue the way a naive
 * clamp-after-the-fact would at extreme lightness.
 */
export function oklchToHex(lch: Oklch): string {
  const maxChroma = maxChromaInGamut(lch.l, lch.h)
  const clamped = { l: lch.l, c: Math.min(lch.c, maxChroma), h: lch.h }
  const rgb255 = linearToRgb255(oklchToLinear(clamped))
  return rgbToHex({
    r: clamp01(rgb255.r / 255) * 255,
    g: clamp01(rgb255.g / 255) * 255,
    b: clamp01(rgb255.b / 255) * 255,
  })
}
