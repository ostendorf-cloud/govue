// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'

import { tokens } from '../styles/contrast.ts'
import { buildPalette } from './palette.ts'
import { renderTheme } from './render.ts'

// Fictional seeds, distinct from every other test file's: a muted teal and
// a dusty rose, neither resembling a real brand.
const CONFIG = { name: 'fixture-render', seeds: { neutral: '#2c5f5a', accent: '#a65d6e' } }

const BASE_ROLES = [
  'bg',
  'bg-subtle',
  'surface',
  'surface-hover',
  'border',
  'border-strong',
  'text',
  'text-muted',
  'text-faint',
  'accent',
  'accent-text',
  'accent-strong',
  'accent-strong-hover',
  'accent-on',
  'accent-subtle',
  'focus',
  'success-text',
  'success-subtle',
  'warning-text',
  'warning-subtle',
  'danger-text',
  'danger-subtle',
  'danger-strong',
  'danger-strong-hover',
  'danger-on',
  'info-text',
  'info-subtle',
]

describe('renderTheme', () => {
  const result = buildPalette(CONFIG)
  if (!result.ok) {
    throw new Error('expected buildPalette to succeed for this fixture')
  }
  const css = renderTheme({
    name: CONFIG.name,
    light: result.light,
    dark: result.dark,
    ink: result.ink,
  })

  it('selects the configured theme name', () => {
    expect(css).toContain(`:root[data-theme='${CONFIG.name}']`)
  })

  it('declares every role base.css declares', () => {
    for (const role of BASE_ROLES) {
      expect(css).toContain(`--color-${role}:`)
    }
    expect(css).toContain('--color-overlay:')
    expect(css).toContain('--shadow-card:')
    expect(css).toContain('--shadow-dialog:')
  })

  it('round-trips through tokens() back to the solved palette', () => {
    const parsed = tokens(css)
    for (const role of BASE_ROLES) {
      expect(parsed.light[role]).toBe(result.light[role])
      expect(parsed.dark[role]).toBe(result.dark[role])
    }
  })

  it('states plainly that the file does not belong in govue', () => {
    const header = css.split('*/')[0].replace(/\s*\*\s*/g, ' ')
    expect(header).toContain('not meant to be committed to govue')
  })
})
