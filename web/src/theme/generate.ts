// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// The orchestrator: validate a seed config, solve a palette, render it to
// CSS. The CLI at ../../scripts/generate-theme.ts is a thin wrapper around
// this; nothing else needs its own logic tested, this module already has.

import { parseThemeSeedConfig } from './config.ts'
import { buildPalette } from './palette.ts'
import { renderTheme } from './render.ts'

export type GenerateResult =
  { ok: true; css: string; warnings: string[] } | { ok: false; errors: string[] }

export function generateTheme(configInput: unknown): GenerateResult {
  const parsed = parseThemeSeedConfig(configInput)
  if (!parsed.ok) {
    return { ok: false, errors: parsed.errors }
  }

  const palette = buildPalette(parsed.config)
  if (!palette.ok) {
    return { ok: false, errors: palette.errors }
  }

  const css = renderTheme({
    name: parsed.config.name,
    light: palette.light,
    dark: palette.dark,
    ink: palette.ink,
  })

  return { ok: true, css, warnings: palette.warnings }
}
