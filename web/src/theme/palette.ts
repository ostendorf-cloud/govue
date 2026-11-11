// The core generator: turns a validated ThemeSeedConfig into a full light and
// dark palette, one hex value per --color-* role base.css declares.
//
// Every role that has a required WCAG pair is found by binary search on
// OKLCH lightness rather than by a closed-form formula: the mapping from L to
// the WCAG ratio is monotonic but not simple algebra once a gamut clamp is
// involved. See ./color.ts for why OKLCH makes that search reliable, and
// ../styles/contrast.ts for the exact ratio the search targets, imported
// directly so there is one implementation of "what counts as accessible".

import {
  failures,
  ratio,
  TEXT_MINIMUM,
  TEXT_PAIRS,
  NON_TEXT_MINIMUM,
  NON_TEXT_PAIRS,
} from '../styles/contrast.ts'
import { hexToOklch, maxChromaInGamut, oklchToHex } from './color.ts'
import type { ThemeSeedConfig } from './config.ts'

export type Mode = 'light' | 'dark'

/** HexPalette holds exactly the --color-* roles that carry a plain hex value
 *  in both files base.css and the shipped example themes use, which is also
 *  the full set of tokens TEXT_PAIRS and NON_TEXT_PAIRS refer to. */
export type HexPalette = Record<string, string>

export interface Ink {
  /** The very-low-lightness, neutral-hue colour behind the light-mode
   *  overlay and shadows. Not contrast-checked: overlay and the shadows never
   *  appear in TEXT_PAIRS or NON_TEXT_PAIRS. */
  light: string
  /** The dark-mode overlay's own ink. Dark-mode shadows are pure black,
   *  matching every shipped theme, so they need no ink of their own. */
  dark: string
}

export type PaletteResult =
  | { ok: true; light: HexPalette; dark: HexPalette; ink: Ink; warnings: string[] }
  | { ok: false; errors: string[] }

// Default hues for the four semantic roles when a config carries no seed for
// them. These are already public and generic: base.css, sample.css and
// sample-contrast.css all document and use the same four hues, so reusing
// them carries no brand information.
const DEFAULT_HUES = { success: 130, warning: 50, danger: 5, info: 200 }

// Fixed, generic chroma for the four semantic roles. Not seed-derived: a
// seed for these is optional and, when present, donates only its hue, so the
// "how saturated does a warning tint look" question needs its own answer
// regardless of whether a seed exists.
const SEMANTIC_CHROMA = { success: 0.15, warning: 0.13, danger: 0.19, info: 0.12 }

const ITERATIONS = 40
// A small headroom over the raw WCAG minimum so that quantising the solved
// OKLCH colour down to an 8-bit hex channel can never tip a pair back under
// the line on re-verification.
const SAFETY_MARGIN = 1.02

interface SolveOptions {
  hue: number
  /** A fixed chroma, or 'max' to use the widest chroma the gamut allows at
   *  each candidate lightness (focus: "the accent hue at full chroma"). */
  chroma: number | 'max'
  /** Hex colours the candidate must clear minRatio against, all at once. */
  anchors: string[]
  minRatio: number
  /** Light mode roles sit on light anchors and must search darker; dark mode
   *  roles sit on dark anchors and must search lighter. */
  towardDark: boolean
  /** Extra multiplier for a role that needs more headroom than the bare
   *  minimum, e.g. a hover state that has to read as distinctly more
   *  extreme than its resting state. */
  margin?: number
}

interface SolveResult {
  hex: string
  chromaUsed: number
}

/** solve bisects OKLCH lightness for the least extreme value that still
 *  clears minRatio against every anchor. towardDark picks which end of
 *  [0, 1] trivially passes (0, full black) and which trivially fails
 *  (1, full white); the loop narrows from there. This is reliable because
 *  the WCAG ratio moves monotonically as one side's luminance moves away
 *  from a fixed anchor's, and OKLCH's L tracks luminance monotonically for a
 *  fixed hue and chroma. */
function solve(opts: SolveOptions): SolveResult {
  const target = opts.minRatio * SAFETY_MARGIN * (opts.margin ?? 1)

  const evaluate = (l: number): SolveResult & { passes: boolean } => {
    const chroma = opts.chroma === 'max' ? maxChromaInGamut(l, opts.hue) : opts.chroma
    const hex = oklchToHex({ l, c: chroma, h: opts.hue })
    const worst = Math.min(...opts.anchors.map((bg) => ratio(hex, bg)))
    return { hex, chromaUsed: chroma, passes: worst >= target }
  }

  const passing = opts.towardDark ? 0 : 1
  const failing = opts.towardDark ? 1 : 0
  let low = passing
  let high = failing
  let best = evaluate(passing)

  for (let i = 0; i < ITERATIONS; i++) {
    const mid = (low + high) / 2
    const candidate = evaluate(mid)
    if (candidate.passes) {
      low = mid
      best = candidate
    } else {
      high = mid
    }
  }

  return { hex: best.hex, chromaUsed: best.chromaUsed }
}

interface Family {
  hue: number
  chroma: number
}

function neutralFamily(config: ThemeSeedConfig): Family {
  const oklch = hexToOklch(config.seeds.neutral)
  return { hue: oklch.h, chroma: oklch.c }
}

function accentFamily(config: ThemeSeedConfig): Family {
  const oklch = hexToOklch(config.seeds.accent)
  return { hue: oklch.h, chroma: oklch.c }
}

/** semanticHue reads only the hue of an optional seed, or falls back to the
 *  shared, already-public default. */
function semanticHue(seed: string | undefined, fallback: number): number {
  return seed === undefined ? fallback : hexToOklch(seed).h
}

/** fixed renders an OKLCH coordinate straight to hex, for roles that are
 *  anchors or tints rather than something solved against another role. */
function fixed(hue: number, chroma: number, l: number): string {
  return oklchToHex({ l, c: chroma, h: hue })
}

function buildOne(
  mode: Mode,
  config: ThemeSeedConfig,
  warn: (message: string) => void,
): HexPalette {
  const light = mode === 'light'
  const neutral = neutralFamily(config)
  const accent = accentFamily(config)
  const dangerHue = semanticHue(config.seeds.danger, DEFAULT_HUES.danger)
  const successHue = semanticHue(config.seeds.success, DEFAULT_HUES.success)
  const warningHue = semanticHue(config.seeds.warning, DEFAULT_HUES.warning)
  const infoHue = semanticHue(config.seeds.info, DEFAULT_HUES.info)

  // Elevation: bg, bg-subtle, surface, surface-hover have no required pair
  // among themselves, so they are placed by eye on a short ramp near the
  // light or the dark extreme rather than solved. surface is pure white in
  // light mode, matching the convention already in base.css, sample.css and
  // sample-contrast.css.
  const surface = light ? '#ffffff' : fixed(neutral.hue, neutral.chroma * 0.6, 0.19)
  const surfaceHover = light
    ? fixed(neutral.hue, neutral.chroma * 0.5, 0.9)
    : fixed(neutral.hue, neutral.chroma * 0.6, 0.21)
  const bg = light
    ? fixed(neutral.hue, neutral.chroma * 0.55, 0.86)
    : fixed(neutral.hue, neutral.chroma * 0.6, 0.12)
  const bgSubtle = light
    ? fixed(neutral.hue, neutral.chroma * 0.6, 0.83)
    : fixed(neutral.hue, neutral.chroma * 0.6, 0.15)

  const bgAnchors = [bg, bgSubtle, surface, surfaceHover]
  const textBgAnchors = [bg, surface]

  const text = solve({
    hue: neutral.hue,
    chroma: neutral.chroma,
    anchors: bgAnchors,
    minRatio: TEXT_MINIMUM,
    towardDark: light,
    margin: 1.35,
  })
  const textMuted = solve({
    hue: neutral.hue,
    chroma: neutral.chroma,
    anchors: textBgAnchors,
    minRatio: TEXT_MINIMUM,
    towardDark: light,
    margin: 1.15,
  })
  const textFaint = solve({
    hue: neutral.hue,
    chroma: neutral.chroma,
    anchors: textBgAnchors,
    minRatio: TEXT_MINIMUM,
    towardDark: light,
  })
  const borderStrong = solve({
    hue: neutral.hue,
    chroma: neutral.chroma,
    anchors: textBgAnchors,
    minRatio: NON_TEXT_MINIMUM,
    towardDark: light,
    margin: 1.05,
  })

  // border carries no required pair of its own: a reasonable step between bg
  // and border-strong, taken in OKLCH so it stays on the neutral hue.
  const bgOklch = hexToOklch(bg)
  const borderStrongOklch = hexToOklch(borderStrong.hex)
  const border = oklchToHex({
    l: (bgOklch.l + borderStrongOklch.l) / 2,
    c: (bgOklch.c + borderStrongOklch.c) / 2,
    h: neutral.hue,
  })

  // accent-on is a fixed anchor, not solved: pure white in light mode, a
  // low-lightness tint of the accent hue in dark mode (never pure black,
  // matching the shipped examples).
  const accentOn = light ? '#ffffff' : fixed(accent.hue, accent.chroma * 0.4, 0.22)
  const accentSubtle = light
    ? fixed(accent.hue, accent.chroma * 0.5, 0.88)
    : fixed(accent.hue, accent.chroma * 0.5, 0.2)

  const accentStrong = solve({
    hue: accent.hue,
    chroma: accent.chroma,
    anchors: [accentOn],
    minRatio: TEXT_MINIMUM,
    towardDark: light,
    margin: 1.1,
  })
  const accentStrongHover = solve({
    hue: accent.hue,
    chroma: accent.chroma,
    anchors: [accentOn],
    minRatio: TEXT_MINIMUM,
    towardDark: light,
    // A visibly larger margin than accent-strong's, so the hover state reads
    // as a distinct, more extreme step rather than the same colour again.
    margin: 1.1 * 1.3,
  })
  const accentRole = solve({
    hue: accent.hue,
    chroma: accent.chroma,
    anchors: textBgAnchors,
    minRatio: NON_TEXT_MINIMUM,
    towardDark: light,
    margin: 1.05,
  })
  const accentText = solve({
    hue: accent.hue,
    chroma: accent.chroma,
    anchors: [bg, surface, accentSubtle],
    minRatio: TEXT_MINIMUM,
    towardDark: light,
    margin: 1.1,
  })
  // The focus ring uses the accent hue at full chroma, exactly like base.css
  // documents: the widest colour the gamut allows at whatever lightness
  // clears the ratio, not a chroma scaled down from the seed.
  const focus = solve({
    hue: accent.hue,
    chroma: 'max',
    anchors: [bg, surface, surfaceHover],
    minRatio: NON_TEXT_MINIMUM,
    towardDark: light,
    margin: 1.1,
  })

  const dangerOn = light ? '#ffffff' : fixed(dangerHue, SEMANTIC_CHROMA.danger * 0.4, 0.22)
  const dangerSubtle = light
    ? fixed(dangerHue, SEMANTIC_CHROMA.danger * 0.55, 0.9)
    : fixed(dangerHue, SEMANTIC_CHROMA.danger * 0.5, 0.2)
  const dangerStrong = solve({
    hue: dangerHue,
    chroma: SEMANTIC_CHROMA.danger,
    anchors: [dangerOn],
    minRatio: TEXT_MINIMUM,
    towardDark: light,
    margin: 1.1,
  })
  const dangerStrongHover = solve({
    hue: dangerHue,
    chroma: SEMANTIC_CHROMA.danger,
    anchors: [dangerOn],
    minRatio: TEXT_MINIMUM,
    towardDark: light,
    margin: 1.1 * 1.3,
  })
  const dangerText = solve({
    hue: dangerHue,
    chroma: SEMANTIC_CHROMA.danger,
    anchors: [surface, dangerSubtle],
    minRatio: TEXT_MINIMUM,
    towardDark: light,
    margin: 1.1,
  })

  const successSubtle = light
    ? fixed(successHue, SEMANTIC_CHROMA.success * 0.55, 0.9)
    : fixed(successHue, SEMANTIC_CHROMA.success * 0.5, 0.2)
  const successText = solve({
    hue: successHue,
    chroma: SEMANTIC_CHROMA.success,
    anchors: [surface, successSubtle],
    minRatio: TEXT_MINIMUM,
    towardDark: light,
    margin: 1.1,
  })

  const warningSubtle = light
    ? fixed(warningHue, SEMANTIC_CHROMA.warning * 0.55, 0.9)
    : fixed(warningHue, SEMANTIC_CHROMA.warning * 0.5, 0.2)
  const warningText = solve({
    hue: warningHue,
    chroma: SEMANTIC_CHROMA.warning,
    anchors: [surface, warningSubtle],
    minRatio: TEXT_MINIMUM,
    towardDark: light,
    margin: 1.1,
  })

  const infoSubtle = light
    ? fixed(infoHue, SEMANTIC_CHROMA.info * 0.55, 0.9)
    : fixed(infoHue, SEMANTIC_CHROMA.info * 0.5, 0.2)
  const infoText = solve({
    hue: infoHue,
    chroma: SEMANTIC_CHROMA.info,
    anchors: [surface, infoSubtle],
    minRatio: TEXT_MINIMUM,
    towardDark: light,
    margin: 1.1,
  })

  // A role whose solved chroma collapsed well below what was requested has
  // gone visibly grey to satisfy contrast: numerically fine, but not what
  // the seed asked for. Warn once per mode rather than once per role, the
  // point is the same either way.
  const collapsed = [
    ['accent-strong', accentStrong.chromaUsed, accent.chroma],
    ['accent', accentRole.chromaUsed, accent.chroma],
    ['accent-text', accentText.chromaUsed, accent.chroma],
    ['text', text.chromaUsed, neutral.chroma],
  ].filter(
    ([, used, requested]) =>
      (requested as number) > 0.02 && (used as number) < (requested as number) * 0.5,
  )
  if (collapsed.length > 0) {
    warn(
      `${mode} mode: ${collapsed.map(([role]) => role).join(', ')} lost more than half its requested chroma to clear contrast and will look washed out`,
    )
  }

  return {
    bg,
    'bg-subtle': bgSubtle,
    surface,
    'surface-hover': surfaceHover,
    border,
    'border-strong': borderStrong.hex,
    text: text.hex,
    'text-muted': textMuted.hex,
    'text-faint': textFaint.hex,
    accent: accentRole.hex,
    'accent-text': accentText.hex,
    'accent-strong': accentStrong.hex,
    'accent-strong-hover': accentStrongHover.hex,
    'accent-on': accentOn,
    'accent-subtle': accentSubtle,
    focus: focus.hex,
    'success-text': successText.hex,
    'success-subtle': successSubtle,
    'warning-text': warningText.hex,
    'warning-subtle': warningSubtle,
    'danger-text': dangerText.hex,
    'danger-subtle': dangerSubtle,
    'danger-strong': dangerStrong.hex,
    'danger-strong-hover': dangerStrongHover.hex,
    'danger-on': dangerOn,
    'info-text': infoText.hex,
    'info-subtle': infoSubtle,
  }
}

function buildInk(config: ThemeSeedConfig): Ink {
  const neutral = neutralFamily(config)
  return {
    light: fixed(neutral.hue, neutral.chroma * 0.6, 0.22),
    dark: fixed(neutral.hue, neutral.chroma * 0.7, 0.08),
  }
}

/** hueDistance is the shortest angular distance between two hues in degrees. */
function hueDistance(a: number, b: number): number {
  const delta = Math.abs(a - b) % 360
  return delta > 180 ? 360 - delta : delta
}

const ACCENT_TOO_CLOSE_TO_NEUTRAL_DEGREES = 15

/** buildPalette solves and renders both modes, re-verifies the whole result
 *  against the real WCAG check, and only then reports success. A generator
 *  that trusted its own solver without re-checking its output would be
 *  exactly the "looks plausible but never actually tested" shortcut this
 *  work is meant to avoid. */
export function buildPalette(config: ThemeSeedConfig): PaletteResult {
  const warnings: string[] = []
  const warn = (message: string): void => {
    warnings.push(message)
  }

  const neutral = neutralFamily(config)
  const accent = accentFamily(config)
  if (hueDistance(neutral.hue, accent.hue) < ACCENT_TOO_CLOSE_TO_NEUTRAL_DEGREES) {
    warn(
      `the accent seed's hue is within ${ACCENT_TOO_CLOSE_TO_NEUTRAL_DEGREES} degrees of the neutral seed's: the accent will likely pass WCAG numerically but will not read as visually distinct from the neutral ramp`,
    )
  }

  const light = buildOne('light', config, warn)
  const dark = buildOne('dark', config, warn)
  const ink = buildInk(config)

  const errors = [
    ...failures(light, TEXT_PAIRS, TEXT_MINIMUM).map((message) => `light mode, ${message}`),
    ...failures(light, NON_TEXT_PAIRS, NON_TEXT_MINIMUM).map((message) => `light mode, ${message}`),
    ...failures(dark, TEXT_PAIRS, TEXT_MINIMUM).map((message) => `dark mode, ${message}`),
    ...failures(dark, NON_TEXT_PAIRS, NON_TEXT_MINIMUM).map((message) => `dark mode, ${message}`),
  ]
  if (errors.length > 0) {
    return { ok: false, errors }
  }

  return { ok: true, light, dark, ink, warnings }
}
