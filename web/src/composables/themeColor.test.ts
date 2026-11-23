// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { runInThisContext } from 'node:vm'

import { beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'

import { pinThemeColor, THEME_COLOR_ATTRIBUTE } from './themeColor'
import { THEME_STORAGE_KEY } from './themeStorage'
import { resetThemeForTests, useTheme, type ColorMode } from './useTheme'

// The boot script of the Go package themeboot, the file the Go side embeds.
const bootSource = readFileSync(resolve(import.meta.dirname, '../../../themeboot/boot.js'), 'utf8')

const LIGHT = '#0f766e'
const DARK = '#0b1f1c'

/** The two tags favicon.Icons.Links writes with a dark colour. */
function writeTags(): HTMLMetaElement[] {
  document.head.innerHTML =
    `<meta name="theme-color" content="${LIGHT}" media="(prefers-color-scheme: light)">` +
    `<meta name="theme-color" content="${DARK}" media="(prefers-color-scheme: dark)">`
  return Array.from(document.head.querySelectorAll('meta'))
}

const contents = (tags: HTMLMetaElement[]): string[] => tags.map((t) => t.content)

describe('pinThemeColor', () => {
  beforeEach(() => {
    vi.unstubAllGlobals()
    resetThemeForTests()
    localStorage.clear()
    document.head.innerHTML = ''
  })

  it('names the attribute the boot script writes', () => {
    expect(bootSource).toContain(`'${THEME_COLOR_ATTRIBUTE}'`)
  })

  it('pins light and dark and gives system back', () => {
    const tags = writeTags()
    pinThemeColor('dark')
    expect(contents(tags)).toEqual([DARK, DARK])
    pinThemeColor('light')
    expect(contents(tags)).toEqual([LIGHT, LIGHT])
    pinThemeColor('system')
    expect(contents(tags)).toEqual([LIGHT, DARK])
  })

  it('leaves a single tag without a media query alone', () => {
    document.head.innerHTML = `<meta name="theme-color" content="${LIGHT}">`
    for (const mode of ['dark', 'light', 'system'] as ColorMode[]) {
      pinThemeColor(mode)
      expect(document.head.querySelector('meta')?.content).toBe(LIGHT)
    }
  })

  // The boot script is inline and imports nothing, so it mirrors the helper.
  // Both run on the same page here and must leave the same tags.
  it.each(['light', 'dark'] as const)('agrees with the boot script for a stored %s', (mode) => {
    localStorage.setItem(THEME_STORAGE_KEY, JSON.stringify({ theme: 'sample', mode }))
    const booted = writeTags()
    runInThisContext(bootSource)
    const byScript = booted.map((t) => [t.content, t.getAttribute(THEME_COLOR_ATTRIBUTE)])

    const helped = writeTags()
    pinThemeColor(mode)
    expect(helped.map((t) => [t.content, t.getAttribute(THEME_COLOR_ATTRIBUTE)])).toEqual(byScript)
  })
})

describe('useTheme and the theme-color tags', () => {
  beforeEach(() => {
    vi.unstubAllGlobals()
    resetThemeForTests()
    localStorage.clear()
    document.head.innerHTML = ''
  })

  it('re-pins the tags when the mode changes after load', async () => {
    const tags = writeTags()
    const state = useTheme()
    await nextTick()
    expect(contents(tags)).toEqual([LIGHT, DARK])

    state.setMode('dark')
    await nextTick()
    expect(contents(tags)).toEqual([DARK, DARK])

    state.setMode('light')
    await nextTick()
    expect(contents(tags)).toEqual([LIGHT, LIGHT])

    state.setMode('system')
    await nextTick()
    expect(contents(tags)).toEqual([LIGHT, DARK])
  })

  it('gives system back after the boot script pinned a stored mode', async () => {
    vi.stubGlobal('matchMedia', () => ({ matches: false, addEventListener: () => {} }))
    localStorage.setItem(THEME_STORAGE_KEY, JSON.stringify({ theme: 'sample', mode: 'dark' }))
    const tags = writeTags()
    runInThisContext(bootSource)
    expect(contents(tags)).toEqual([DARK, DARK])

    const state = useTheme()
    await nextTick()
    expect(contents(tags)).toEqual([DARK, DARK])

    state.setMode('system')
    await nextTick()
    expect(contents(tags)).toEqual([LIGHT, DARK])
  })
})
