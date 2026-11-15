// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { computed, inject, watch, type App, type ComputedRef, type InjectionKey } from 'vue'
import { createI18n } from 'vue-i18n'

import {
  catalogues,
  DEFAULT_LOCALE,
  isLocaleCode,
  localeCodes,
  type LocaleCode,
} from './catalogues'
import {
  createLanguageStorage,
  type LanguageStorage,
  type LanguageStorageOption,
} from './languageStorage'
import type { MessageTree } from './messages'

export type NamedValues = Record<string, string | number>

/** What the components and the switch talk to. */
export interface LanguageState {
  /** The active language. Reactive. */
  locale: ComputedRef<string>
  /** Every language the switch offers, each named in its own language. */
  locales: ReadonlyArray<{ code: LocaleCode; name: string }>
  /**
   * Switches the language, sets lang on the document and, unless persist is
   * false, stores the choice. A code that is not in the catalogue list is
   * ignored.
   */
  setLocale: (code: string, options?: { persist?: boolean }) => void
  t: (key: string, named?: NamedValues) => string
}

export const languageKey: InjectionKey<LanguageState> = Symbol('govue-language')

/**
 * The part of a vue-i18n instance (composition mode) the plugin needs. It is
 * structural on purpose: an instance built with the app's own message types
 * and locale union fits, which a plain Composer would reject.
 */
export interface ExistingI18n {
  global: {
    locale: { value: string }
    mergeLocaleMessage(locale: string, messages: MessageTree): void
    t(key: string, named?: NamedValues): string
  }
  install: (app: App) => void
}

export interface GovueI18nOptions {
  /**
   * Share the app's own vue-i18n instance (created with legacy: false) instead
   * of creating one. govue merges its catalogues into it under the govue key.
   * The app installs its own instance itself.
   */
  i18n?: ExistingI18n
  /** Forces the starting language and skips detection. */
  locale?: string
  /**
   * Run the choice order (stored, browser, default) at install. Default: true
   * for an instance govue creates, false for one passed in, which keeps its
   * own locale.
   */
  detect?: boolean
  /** Where the choice is kept. Default: localStorage under govue-language. */
  storage?: LanguageStorageOption
  /** The app's own messages, merged after govue's, so a key can be overridden. */
  messages?: Partial<Record<string, MessageTree>>
}

export interface GovueI18n {
  install: (app: App) => void
  /** The vue-i18n instance in use. Call mergeLocaleMessage on its global. */
  i18n: ExistingI18n
  language: LanguageState
}

/**
 * pickLocale applies the choice order: a stored value, then the first browser
 * language we have, then German. A stored value outside the catalogue list is
 * ignored. Region codes count by their language: de-AT is de.
 */
export function pickLocale(stored: string | undefined, browser: readonly string[]): LocaleCode {
  if (isLocaleCode(stored)) {
    return stored
  }
  for (const tag of browser) {
    const primary = tag.split('-')[0]?.toLowerCase()
    if (isLocaleCode(primary)) {
      return primary
    }
  }
  return DEFAULT_LOCALE
}

function browserLanguages(): readonly string[] {
  if (typeof navigator === 'undefined') {
    return []
  }
  if (navigator.languages && navigator.languages.length > 0) {
    return navigator.languages
  }
  return navigator.language ? [navigator.language] : []
}

/**
 * createGovueI18n sets up the language layer: the govue catalogues, the
 * choice order, the persistence and lang on the document element.
 *
 *   app.use(createGovueI18n())
 */
export function createGovueI18n(options: GovueI18nOptions = {}): GovueI18n {
  const own = options.i18n === undefined
  const i18n: ExistingI18n =
    options.i18n ??
    createI18n<[MessageTree], string, false>({
      legacy: false,
      locale: DEFAULT_LOCALE,
      fallbackLocale: DEFAULT_LOCALE,
      messages: {},
    })
  const composer = i18n.global
  const storage: LanguageStorage = createLanguageStorage(options.storage)

  for (const code of localeCodes) {
    composer.mergeLocaleMessage(code, catalogues[code].messages)
  }
  for (const [code, messages] of Object.entries(options.messages ?? {})) {
    if (messages) {
      composer.mergeLocaleMessage(code, messages)
    }
  }

  // A server or a prerender has no visitor. Node 21 and later define a global
  // navigator whose languages follow the build host, and newer Node versions
  // a global localStorage, so the test is for a window, not for those
  // globals: without one, the language is the default.
  const browserless = typeof window === 'undefined'
  const detect = options.detect ?? own
  if (options.locale !== undefined) {
    // An unknown code falls back like setLocale ignores it: to the default.
    composer.locale.value = isLocaleCode(options.locale) ? options.locale : DEFAULT_LOCALE
  } else if (detect) {
    composer.locale.value = browserless
      ? DEFAULT_LOCALE
      : pickLocale(storage.read(), browserLanguages())
  }

  // lang follows the locale however it changed: the switch, the app's router
  // or a direct write to the instance.
  const apply = (code: string): void => {
    if (typeof document !== 'undefined') {
      document.documentElement.lang = code
    }
  }
  watch(() => composer.locale.value, apply, { immediate: true })

  const language: LanguageState = {
    locale: computed(() => composer.locale.value),
    locales: localeCodes.map((code) => ({ code, name: catalogues[code].name })),
    setLocale: (code, opts) => {
      if (!isLocaleCode(code)) {
        return
      }
      composer.locale.value = code
      if (opts?.persist !== false) {
        storage.write(code)
      }
    },
    t: (key, named) => (named ? composer.t(key, named) : composer.t(key)),
  }

  return {
    i18n,
    language,
    install(app) {
      if (own) {
        app.use(i18n)
      }
      app.provide(languageKey, language)
    },
  }
}

/**
 * useLanguage is the switch's door to the layer. Without the plugin there is
 * nothing to switch, which is a setup mistake, so it throws.
 */
export function useLanguage(): LanguageState {
  const state = inject(languageKey, null)
  if (!state) {
    throw new Error('govue: install the language layer first: app.use(createGovueI18n())')
  }
  return state
}

/**
 * useGovueText gives the components their default texts. Without the plugin
 * it answers from the English catalogue, so an app that never installed the
 * language layer sees exactly what it saw before it existed.
 */
export function useGovueText(): { t: LanguageState['t'] } {
  const state = inject(languageKey, null)
  return { t: state ? state.t : englishText }
}

function englishText(key: string, named?: NamedValues): string {
  let node: string | MessageTree | undefined = catalogues.en.messages
  for (const part of key.split('.')) {
    node = typeof node === 'object' ? node[part] : undefined
  }
  if (typeof node !== 'string') {
    return key
  }
  return node.replace(/\{(\w+)\}/g, (match, name: string) =>
    named && name in named ? String(named[name]) : match,
  )
}
