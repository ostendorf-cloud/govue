// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'

import { resetThemeForTests, useTheme } from './useTheme'

/** stubMatchMedia makes the system setting controllable. */
function stubMatchMedia(prefersDark: boolean): (matches: boolean) => void {
  const listeners: Array<(event: MediaQueryListEvent) => void> = []
  let matches = prefersDark
  vi.stubGlobal('matchMedia', (query: string) => ({
    media: query,
    get matches() {
      return matches
    },
    addEventListener: (_type: string, listener: (event: MediaQueryListEvent) => void) => {
      listeners.push(listener)
    },
    removeEventListener: () => {},
  }))
  return (next: boolean) => {
    matches = next
    for (const listener of listeners) {
      listener({ matches: next } as MediaQueryListEvent)
    }
  }
}

describe('useTheme', () => {
  beforeEach(() => {
    // The storage and matchMedia stubs of one test must not reach the next.
    vi.unstubAllGlobals()
    resetThemeForTests()
    localStorage.clear()
    delete document.documentElement.dataset.theme
    delete document.documentElement.dataset.mode
    stubMatchMedia(false)
  })

  it('writes the theme and leaves the mode to the system by default', async () => {
    const { theme, mode, resolvedMode } = useTheme({ theme: 'sample-contrast' })
    await nextTick()

    expect(theme.value).toBe('sample-contrast')
    expect(mode.value).toBe('system')
    expect(document.documentElement.dataset.theme).toBe('sample-contrast')
    // No attribute is the third state: the stylesheet decides with the system.
    expect(document.documentElement.dataset.mode).toBeUndefined()
    expect(resolvedMode.value).toBe('light')
  })

  it('follows the system setting while the mode is system', async () => {
    const change = stubMatchMedia(true)
    const { resolvedMode } = useTheme()
    await nextTick()

    expect(resolvedMode.value).toBe('dark')

    change(false)
    await nextTick()
    expect(resolvedMode.value).toBe('light')
  })

  it('pins the mode against the system in both directions', async () => {
    // The system says dark, so an explicit light has to win, and an explicit
    // dark has to stay dark once the system flips back to light.
    const change = stubMatchMedia(true)
    const { setMode, resolvedMode } = useTheme()

    setMode('light')
    await nextTick()
    expect(document.documentElement.dataset.mode).toBe('light')
    expect(resolvedMode.value).toBe('light')

    setMode('dark')
    change(false)
    await nextTick()
    expect(document.documentElement.dataset.mode).toBe('dark')
    expect(resolvedMode.value).toBe('dark')

    setMode('system')
    await nextTick()
    expect(document.documentElement.dataset.mode).toBeUndefined()
    expect(resolvedMode.value).toBe('light')
  })

  it('cycles light, dark and back to the system', async () => {
    const { mode, cycleMode } = useTheme({ mode: 'light' })

    cycleMode()
    expect(mode.value).toBe('dark')
    cycleMode()
    expect(mode.value).toBe('system')
    cycleMode()
    expect(mode.value).toBe('light')
    await nextTick()
  })

  it('persists the choice and restores it', async () => {
    const first = useTheme({ theme: 'sample' })
    first.setTheme('sample-contrast')
    first.setMode('dark')
    await nextTick()

    resetThemeForTests()
    const second = useTheme({ theme: 'sample', mode: 'system' })
    await nextTick()

    expect(second.theme.value).toBe('sample-contrast')
    expect(second.mode.value).toBe('dark')
    expect(document.documentElement.dataset.theme).toBe('sample-contrast')
  })

  it('takes a theme name it does not ship', async () => {
    // An app's own theme fills the same contract and is never listed here.
    const { theme } = useTheme({ theme: 'an-app-of-its-own' })
    await nextTick()

    expect(theme.value).toBe('an-app-of-its-own')
    expect(document.documentElement.dataset.theme).toBe('an-app-of-its-own')
  })

  it('ignores stored nonsense', async () => {
    localStorage.setItem('web-kit-theme', '{"theme":42,"mode":"sepia"}')

    const { theme, mode } = useTheme({ theme: 'sample-contrast' })
    await nextTick()

    expect(theme.value).toBe('sample-contrast')
    expect(mode.value).toBe('system')
  })

  it('works when storage throws', async () => {
    const broken = {
      getItem: () => {
        throw new Error('blocked')
      },
      setItem: () => {
        throw new Error('blocked')
      },
    }
    vi.stubGlobal('localStorage', broken)

    const { setMode, mode } = useTheme({ theme: 'sample' })
    setMode('dark')
    await nextTick()

    expect(mode.value).toBe('dark')
    expect(document.documentElement.dataset.mode).toBe('dark')
  })

  it('shares one state across callers', async () => {
    const first = useTheme({ theme: 'sample' })
    const second = useTheme()

    first.setTheme('sample-contrast')
    await nextTick()

    expect(second.theme.value).toBe('sample-contrast')
  })
})
