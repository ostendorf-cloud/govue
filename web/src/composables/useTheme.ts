// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { computed, readonly, ref, watch, type ComputedRef, type Ref } from 'vue'

/**
 * The example themes this package ships. An app fills the same contract in its
 * own repository and passes that theme's name, so the type stays open.
 */
export const themeNames = ['sample', 'sample-contrast'] as const

export type ThemeName = (typeof themeNames)[number] | (string & {})

/** system follows the operating system; the other two are the visitor's choice. */
export type ColorMode = 'light' | 'dark' | 'system'

export interface ThemeOptions {
  /** Which theme the page starts with, when nothing is stored. */
  theme?: ThemeName
  /** Which mode the page starts with, when nothing is stored. */
  mode?: ColorMode
  /** Where the choice is kept. One key per app. */
  storageKey?: string
}

export interface ThemeState {
  theme: Readonly<Ref<ThemeName>>
  /** What the visitor chose, system included. */
  mode: Readonly<Ref<ColorMode>>
  /** What that means right now: never "system". */
  resolvedMode: ComputedRef<'light' | 'dark'>
  setTheme: (next: ThemeName) => void
  setMode: (next: ColorMode) => void
  /** Steps light, dark, system, in that order. */
  cycleMode: () => void
}

const DEFAULT_STORAGE_KEY = 'govue-theme'
const DEFAULT_THEME: ThemeName = 'sample'

// One page has one theme, so the state is shared rather than created per
// component. The first call decides the defaults and the storage key.
const theme = ref<ThemeName>(DEFAULT_THEME)
const mode = ref<ColorMode>('system')
const systemPrefersDark = ref(false)
let storageKey = DEFAULT_STORAGE_KEY
let started = false

/**
 * useTheme reads and writes the two attributes the stylesheets switch on:
 * data-theme picks the palette, data-mode pins light or dark against the system
 * setting. The choice is persisted, so it survives a reload.
 *
 * The mode has three states and the third one is the absence of an attribute:
 * light and dark are the visitor's own choice and win over the system in both
 * directions, system removes the attribute and lets light-dark() follow the
 * operating system again.
 */
export function useTheme(options: ThemeOptions = {}): ThemeState {
  if (!started) {
    started = true
    storageKey = options.storageKey ?? DEFAULT_STORAGE_KEY
    theme.value = options.theme ?? theme.value
    mode.value = options.mode ?? mode.value
    restore()
    watchSystem()
    watch([theme, mode], apply, { immediate: true })
  }

  return {
    theme: readonly(theme),
    mode: readonly(mode),
    resolvedMode: computed(() =>
      mode.value === 'system' ? (systemPrefersDark.value ? 'dark' : 'light') : mode.value,
    ),
    setTheme: (next) => {
      theme.value = next
      persist()
    },
    setMode: (next) => {
      mode.value = next
      persist()
    },
    cycleMode: () => {
      const order: ColorMode[] = ['light', 'dark', 'system']
      mode.value = order[(order.indexOf(mode.value) + 1) % order.length]
      persist()
    },
  }
}

/** resetThemeForTests drops the shared state. Tests only. */
export function resetThemeForTests(): void {
  started = false
  storageKey = DEFAULT_STORAGE_KEY
  theme.value = DEFAULT_THEME
  mode.value = 'system'
  systemPrefersDark.value = false
}

function apply(): void {
  if (typeof document === 'undefined') {
    return
  }
  const root = document.documentElement
  root.dataset.theme = theme.value
  if (mode.value === 'system') {
    // No attribute is the third state: the stylesheets then let the system
    // setting decide through light-dark().
    delete root.dataset.mode
  } else {
    root.dataset.mode = mode.value
  }
}

function watchSystem(): void {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') {
    return
  }
  const query = window.matchMedia('(prefers-color-scheme: dark)')
  systemPrefersDark.value = query.matches
  query.addEventListener('change', (event) => {
    systemPrefersDark.value = event.matches
  })
}

/** restore reads the stored choice. Storage can throw or be empty, and a page
 *  with no stored choice is the normal case, so neither is an error. */
function restore(): void {
  try {
    const raw = globalThis.localStorage?.getItem(storageKey)
    if (!raw) {
      return
    }
    const stored = JSON.parse(raw) as { theme?: unknown; mode?: unknown }
    if (typeof stored.theme === 'string' && stored.theme !== '') {
      theme.value = stored.theme
    }
    if (stored.mode === 'light' || stored.mode === 'dark' || stored.mode === 'system') {
      mode.value = stored.mode
    }
  } catch {
    // A private window, cleared site data, or storage that is blocked.
  }
}

function persist(): void {
  try {
    globalThis.localStorage?.setItem(
      storageKey,
      JSON.stringify({ theme: theme.value, mode: mode.value }),
    )
  } catch {
    // The choice then only lasts for this page, which is better than failing.
  }
}
