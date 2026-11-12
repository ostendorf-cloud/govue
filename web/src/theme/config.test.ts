import { describe, expect, it } from 'vitest'

import { parseThemeSeedConfig } from './config.ts'

// Fictional, round, obviously-fabricated seeds, distinct from every value in
// base.css, sample.css and sample-contrast.css, and from color.test.ts's.
const VALID_SEEDS = { neutral: '#334455', accent: '#aa6611' }

describe('parseThemeSeedConfig', () => {
  it('accepts a valid config with only the required seeds', () => {
    const result = parseThemeSeedConfig({ name: 'acme', seeds: VALID_SEEDS })
    expect(result.ok).toBe(true)
    if (result.ok) {
      expect(result.config).toEqual({
        name: 'acme',
        seeds: {
          neutral: '#334455',
          accent: '#aa6611',
          success: undefined,
          warning: undefined,
          danger: undefined,
          info: undefined,
        },
      })
    }
  })

  it('accepts every optional seed alongside the required two', () => {
    const result = parseThemeSeedConfig({
      name: 'acme',
      seeds: {
        ...VALID_SEEDS,
        success: '#227733',
        warning: '#998800',
        danger: '#991100',
        info: '#114488',
      },
    })
    expect(result.ok).toBe(true)
  })

  it('rejects a missing neutral seed, naming the field', () => {
    const result = parseThemeSeedConfig({ name: 'acme', seeds: { accent: '#aa6611' } })
    expect(result.ok).toBe(false)
    if (!result.ok) {
      expect(result.errors).toEqual([
        'seeds.neutral: missing, a neutral and an accent seed are both required',
      ])
    }
  })

  it('rejects a missing accent seed, naming the field', () => {
    const result = parseThemeSeedConfig({ name: 'acme', seeds: { neutral: '#334455' } })
    expect(result.ok).toBe(false)
    if (!result.ok) {
      expect(result.errors).toEqual([
        'seeds.accent: missing, a neutral and an accent seed are both required',
      ])
    }
  })

  it('rejects a malformed hex value, naming the field and the bad value', () => {
    const result = parseThemeSeedConfig({
      name: 'acme',
      seeds: { neutral: 'blue', accent: '#aa6611' },
    })
    expect(result.ok).toBe(false)
    if (!result.ok) {
      expect(result.errors).toEqual(['seeds.neutral: "blue" is not a valid #rrggbb hex colour'])
    }
  })

  it('rejects a hex value missing its leading #', () => {
    const result = parseThemeSeedConfig({
      name: 'acme',
      seeds: { neutral: '334455', accent: '#aa6611' },
    })
    expect(result.ok).toBe(false)
  })

  it('rejects an empty name', () => {
    const result = parseThemeSeedConfig({ name: '', seeds: VALID_SEEDS })
    expect(result.ok).toBe(false)
    if (!result.ok) {
      expect(result.errors).toEqual(['name: must be a non-empty string'])
    }
  })

  it('rejects a name that is not a lowercase slug', () => {
    const result = parseThemeSeedConfig({ name: 'Acme Corp', seeds: VALID_SEEDS })
    expect(result.ok).toBe(false)
    if (!result.ok) {
      expect(result.errors[0]).toContain('name:')
    }
  })

  it('rejects an unknown seed field', () => {
    const result = parseThemeSeedConfig({
      name: 'acme',
      seeds: { ...VALID_SEEDS, brand: '#ffffff' },
    })
    expect(result.ok).toBe(false)
    if (!result.ok) {
      expect(result.errors).toEqual([expect.stringContaining('seeds.brand: unknown seed')])
    }
  })

  it('collects every error at once instead of stopping at the first', () => {
    const result = parseThemeSeedConfig({ name: '', seeds: { neutral: 'nope' } })
    expect(result.ok).toBe(false)
    if (!result.ok) {
      expect(result.errors.length).toBeGreaterThan(1)
    }
  })

  it('rejects a config that is not an object', () => {
    expect(parseThemeSeedConfig(null).ok).toBe(false)
    expect(parseThemeSeedConfig('acme').ok).toBe(false)
    expect(parseThemeSeedConfig([]).ok).toBe(false)
  })

  it('rejects a config whose seeds field is not an object', () => {
    const result = parseThemeSeedConfig({ name: 'acme', seeds: 'acme' })
    expect(result.ok).toBe(false)
  })
})
