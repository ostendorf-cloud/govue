// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// The seed configuration a consumer writes, and the only input the generator
// needs. It stays in the consumer's own repository: this module only
// describes its shape and checks it, it never stores a real one.

export interface ThemeSeedConfig {
  /** Becomes :root[data-theme='<name>']. */
  name: string
  seeds: {
    /** Donates hue and chroma to the bg/border/text ramp. */
    neutral: string
    /** Donates hue and chroma to the accent/focus-ring family. */
    accent: string
    /** Hue only. Defaults to 130, the hue base.css already documents. */
    success?: string
    /** Hue only. Defaults to 50, the hue base.css already documents. */
    warning?: string
    /** Hue only. Defaults to 5, the hue base.css already documents. */
    danger?: string
    /** Hue only. Defaults to 200, the hue base.css already documents. */
    info?: string
  }
}

export type ConfigResult = { ok: true; config: ThemeSeedConfig } | { ok: false; errors: string[] }

const HEX_PATTERN = /^#[0-9a-fA-F]{6}$/
// A name becomes an attribute value and a CSS selector segment, so it stays
// inside the same slug shape the shipped theme names already use.
const NAME_PATTERN = /^[a-z0-9]+(-[a-z0-9]+)*$/

const REQUIRED_SEEDS = ['neutral', 'accent'] as const
const OPTIONAL_SEEDS = ['success', 'warning', 'danger', 'info'] as const

function isHex(value: unknown): value is string {
  return typeof value === 'string' && HEX_PATTERN.test(value)
}

/** describe renders an arbitrary JSON value for an error message. JSON.stringify
 *  rather than template interpolation or String(), so an object or an array in
 *  the config does not silently become "[object Object]". */
function describe(value: unknown): string {
  if (typeof value === 'string') {
    return value
  }
  return JSON.stringify(value) ?? 'undefined'
}

/** parseThemeSeedConfig validates an arbitrary JSON value against the seed
 *  config shape, naming every field it rejects rather than throwing on the
 *  first one. */
export function parseThemeSeedConfig(input: unknown): ConfigResult {
  const errors: string[] = []

  if (typeof input !== 'object' || input === null || Array.isArray(input)) {
    return { ok: false, errors: ['config: must be a JSON object'] }
  }
  const root = input as Record<string, unknown>

  if (typeof root.name !== 'string' || root.name === '') {
    errors.push('name: must be a non-empty string')
  } else if (!NAME_PATTERN.test(root.name)) {
    errors.push(
      `name: "${root.name}" must be a lowercase slug (letters, digits and hyphens, e.g. "acme")`,
    )
  }

  if (typeof root.seeds !== 'object' || root.seeds === null || Array.isArray(root.seeds)) {
    errors.push('seeds: must be an object')
    return { ok: false, errors }
  }
  const seeds = root.seeds as Record<string, unknown>

  for (const field of REQUIRED_SEEDS) {
    const value = seeds[field]
    if (value === undefined) {
      errors.push(`seeds.${field}: missing, a neutral and an accent seed are both required`)
    } else if (!isHex(value)) {
      errors.push(`seeds.${field}: "${describe(value)}" is not a valid #rrggbb hex colour`)
    }
  }

  for (const field of OPTIONAL_SEEDS) {
    const value = seeds[field]
    if (value !== undefined && !isHex(value)) {
      errors.push(`seeds.${field}: "${describe(value)}" is not a valid #rrggbb hex colour`)
    }
  }

  const knownFields = new Set<string>([...REQUIRED_SEEDS, ...OPTIONAL_SEEDS])
  for (const field of Object.keys(seeds)) {
    if (!knownFields.has(field)) {
      errors.push(`seeds.${field}: unknown seed, expected one of ${[...knownFields].join(', ')}`)
    }
  }

  if (errors.length > 0) {
    return { ok: false, errors }
  }

  return {
    ok: true,
    config: {
      name: root.name as string,
      seeds: {
        neutral: seeds.neutral as string,
        accent: seeds.accent as string,
        success: seeds.success as string | undefined,
        warning: seeds.warning as string | undefined,
        danger: seeds.danger as string | undefined,
        info: seeds.info as string | undefined,
      },
    },
  }
}
