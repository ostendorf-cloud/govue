// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { runInThisContext } from 'node:vm'

import { beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'

import { MODE_ATTRIBUTE, THEME_ATTRIBUTE, THEME_STORAGE_KEY } from './themeStorage'
import { resetThemeForTests, useTheme } from './useTheme'

// The script the Go package themeboot injects into the page. It is read from
// the file the Go side embeds, so this tests the shipped script itself.
const bootSource = readFileSync(resolve(import.meta.dirname, '../../../themeboot/boot.js'), 'utf8')

/** boot runs the script the way the browser does, before any bundle. */
function boot(): void {
  runInThisContext(bootSource)
}

const root = document.documentElement

describe('the theme boot script', () => {
  beforeEach(() => {
    vi.unstubAllGlobals()
    resetThemeForTests()
    localStorage.clear()
    root.removeAttribute(THEME_ATTRIBUTE)
    root.removeAttribute(MODE_ATTRIBUTE)
  })

  it('uses the names the composable shares', () => {
    expect(bootSource).toContain(`'${THEME_STORAGE_KEY}'`)
    expect(bootSource).toContain(`'${THEME_ATTRIBUTE}'`)
    expect(bootSource).toContain(`'${MODE_ATTRIBUTE}'`)
  })

  it('pins dark when dark is stored', () => {
    localStorage.setItem(THEME_STORAGE_KEY, JSON.stringify({ theme: 'sample', mode: 'dark' }))

    boot()

    expect(root.getAttribute(THEME_ATTRIBUTE)).toBe('sample')
    expect(root.getAttribute(MODE_ATTRIBUTE)).toBe('dark')
  })

  it('pins light against a dark system when light is stored', () => {
    vi.stubGlobal('matchMedia', () => ({ matches: true }))
    localStorage.setItem(
      THEME_STORAGE_KEY,
      JSON.stringify({ theme: 'sample-contrast', mode: 'light' }),
    )

    boot()

    expect(root.getAttribute(THEME_ATTRIBUTE)).toBe('sample-contrast')
    expect(root.getAttribute(MODE_ATTRIBUTE)).toBe('light')
  })

  it('leaves the mode to a dark system when nothing is stored', () => {
    vi.stubGlobal('matchMedia', () => ({ matches: true }))

    boot()

    // The stylesheet follows the system through light-dark() while the
    // attribute is absent, so no attribute is the correct start.
    expect(root.getAttribute(THEME_ATTRIBUTE)).toBe('sample')
    expect(root.hasAttribute(MODE_ATTRIBUTE)).toBe(false)
  })

  it('removes a stale mode attribute when the choice is system', () => {
    root.setAttribute(MODE_ATTRIBUTE, 'dark')
    localStorage.setItem(THEME_STORAGE_KEY, JSON.stringify({ theme: 'sample', mode: 'system' }))

    boot()

    expect(root.hasAttribute(MODE_ATTRIBUTE)).toBe(false)
  })

  it('keeps a theme the server rendered when nothing is stored', () => {
    root.setAttribute(THEME_ATTRIBUTE, 'an-app-of-its-own')

    boot()

    expect(root.getAttribute(THEME_ATTRIBUTE)).toBe('an-app-of-its-own')
  })

  it('survives nonsense and blocked storage', () => {
    localStorage.setItem(THEME_STORAGE_KEY, '{not json')
    boot()
    expect(root.getAttribute(THEME_ATTRIBUTE)).toBe('sample')

    vi.stubGlobal('localStorage', {
      getItem: () => {
        throw new Error('blocked')
      },
    })
    boot()
    expect(root.getAttribute(THEME_ATTRIBUTE)).toBe('sample')
    expect(root.hasAttribute(MODE_ATTRIBUTE)).toBe(false)
  })

  describe('the theme-color tags', () => {
    const LIGHT = '#0f766e'
    const DARK = '#0b1f1c'

    /** The two tags favicon.Icons.Links writes with a dark colour. */
    function writeTags(): HTMLMetaElement[] {
      document.head.innerHTML =
        `<meta name="theme-color" content="${LIGHT}" media="(prefers-color-scheme: light)">` +
        `<meta name="theme-color" content="${DARK}" media="(prefers-color-scheme: dark)">`
      return Array.from(document.head.querySelectorAll('meta'))
    }

    beforeEach(() => {
      document.head.innerHTML = ''
    })

    it('leaves both tags to the system when nothing is stored', () => {
      const tags = writeTags()
      boot()
      expect(tags.map((t) => t.content)).toEqual([LIGHT, DARK])
    })

    it('pins both tags to dark when dark is stored', () => {
      localStorage.setItem(THEME_STORAGE_KEY, JSON.stringify({ theme: 'sample', mode: 'dark' }))
      const tags = writeTags()
      boot()
      expect(tags.map((t) => t.content)).toEqual([DARK, DARK])
    })

    it('pins both tags to light when light is stored against a dark system', () => {
      vi.stubGlobal('matchMedia', () => ({ matches: true }))
      localStorage.setItem(THEME_STORAGE_KEY, JSON.stringify({ theme: 'sample', mode: 'light' }))
      const tags = writeTags()
      boot()
      expect(tags.map((t) => t.content)).toEqual([LIGHT, LIGHT])
    })

    it('leaves both tags alone for the system choice', () => {
      localStorage.setItem(THEME_STORAGE_KEY, JSON.stringify({ theme: 'sample', mode: 'system' }))
      const tags = writeTags()
      boot()
      expect(tags.map((t) => t.content)).toEqual([LIGHT, DARK])
    })

    it('leaves a single tag without a media query alone', () => {
      localStorage.setItem(THEME_STORAGE_KEY, JSON.stringify({ theme: 'sample', mode: 'dark' }))
      document.head.innerHTML = `<meta name="theme-color" content="${LIGHT}">`
      boot()
      expect(document.head.querySelector('meta')?.content).toBe(LIGHT)
    })

    it('ignores a stored mode outside light and dark', () => {
      localStorage.setItem(
        THEME_STORAGE_KEY,
        JSON.stringify({ theme: 'sample', mode: 'dark) or (x' }),
      )
      const tags = writeTags()
      boot()
      expect(tags.map((t) => t.content)).toEqual([LIGHT, DARK])
    })
  })

  it('agrees with what useTheme stores and applies', async () => {
    const first = useTheme({ theme: 'sample-contrast' })
    first.setMode('dark')
    await nextTick()
    const applied = [root.getAttribute(THEME_ATTRIBUTE), root.getAttribute(MODE_ATTRIBUTE)]

    root.removeAttribute(THEME_ATTRIBUTE)
    root.removeAttribute(MODE_ATTRIBUTE)
    boot()

    expect([root.getAttribute(THEME_ATTRIBUTE), root.getAttribute(MODE_ATTRIBUTE)]).toEqual(applied)
  })
})
