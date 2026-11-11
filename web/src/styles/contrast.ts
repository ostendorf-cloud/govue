// WCAG 2.2 AA contrast math, shared by contrast.test.ts and the theme
// generator under ../theme/. One implementation of "what counts as
// accessible" rather than two that could drift: the generator validates its
// own output through the exact same ratio() and failures() the test suite
// checks every shipped theme with.

// The pairs that have to hold in every theme, in both modes. A theme is only
// finished when this passes: a palette that looks right and fails here is not
// usable, and checking it by eye does not work.
//
// 4.5 is WCAG 2.2 AA for body text, 3 for a boundary, an icon or a focus ring.
export const TEXT_MINIMUM = 4.5
export const NON_TEXT_MINIMUM = 3

export const TEXT_PAIRS: Array<[fg: string, bg: string]> = [
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

export const NON_TEXT_PAIRS: Array<[fg: string, bg: string]> = [
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

export type Palette = Record<string, string>

/** tokens reads the hex token pairs of one stylesheet, one map per mode. */
export function tokens(css: string): { light: Palette; dark: Palette } {
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
export function channel(value: number): number {
  const srgb = value / 255
  return srgb <= 0.03928 ? srgb / 12.92 : ((srgb + 0.055) / 1.055) ** 2.4
}

export function luminance(hex: string): number {
  const value = Number.parseInt(hex.slice(1), 16)
  const r = channel((value >> 16) & 0xff)
  const g = channel((value >> 8) & 0xff)
  const b = channel(value & 0xff)
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}

export function ratio(foreground: string, background: string): number {
  const a = luminance(foreground)
  const b = luminance(background)
  const [lighter, darker] = a > b ? [a, b] : [b, a]
  return (lighter + 0.05) / (darker + 0.05)
}

/** round keeps a failure message readable. */
export function round(value: number): number {
  return Math.round(value * 100) / 100
}

/** failures lists the pairs of one palette that stay under the minimum. A pair
 *  with a token the palette never declares is reported as such: the ratio
 *  cannot be computed, and saying which token is missing beats a TypeError. */
export function failures(
  palette: Palette,
  pairs: Array<[fg: string, bg: string]>,
  minimum: number,
): string[] {
  return pairs.flatMap(([fg, bg]) => {
    const missing = [fg, bg].filter((token) => palette[token] === undefined)
    if (missing.length > 0) {
      return [`${fg} on ${bg} cannot be checked, ${missing.join(' and ')} is not defined`]
    }
    const value = ratio(palette[fg], palette[bg])
    return value < minimum ? [`${fg} on ${bg} is ${round(value)}:1, want ${minimum}:1`] : []
  })
}
