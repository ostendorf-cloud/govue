// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp } from 'vue'
import { createI18n } from 'vue-i18n'

import { LANGUAGE_STORAGE_KEY } from './languageStorage'
import { createGovueI18n, pickLocale } from './plugin'

function browser(...languages: string[]): void {
  vi.stubGlobal('navigator', { languages, language: languages[0] })
}

beforeEach(() => {
  vi.unstubAllGlobals()
  localStorage.clear()
  document.cookie = 'govue-language=; Path=/; Max-Age=0'
  document.documentElement.removeAttribute('lang')
  browser('en-US')
})

describe('pickLocale', () => {
  it('prefers a stored value over the browser', () => {
    expect(pickLocale('de', ['en'])).toBe('de')
  })

  it('prefers the browser over the default', () => {
    expect(pickLocale(undefined, ['en'])).toBe('en')
  })

  it('falls back to German', () => {
    expect(pickLocale(undefined, [])).toBe('de')
  })

  it('ignores a stored value it does not know', () => {
    expect(pickLocale('fr', ['en'])).toBe('en')
    expect(pickLocale('constructor', [])).toBe('de')
  })

  it('reads a region code by its language', () => {
    expect(pickLocale(undefined, ['de-AT'])).toBe('de')
    expect(pickLocale(undefined, ['en-GB'])).toBe('en')
    expect(pickLocale(undefined, ['EN-us'])).toBe('en')
  })

  it('falls back to German for an unsupported language', () => {
    expect(pickLocale(undefined, ['fr', 'fr-CA'])).toBe('de')
  })

  it('takes the first supported language in the browser order', () => {
    expect(pickLocale(undefined, ['fr', 'en-GB', 'de'])).toBe('en')
  })
})

describe('createGovueI18n', () => {
  it('starts from the stored choice', () => {
    localStorage.setItem(LANGUAGE_STORAGE_KEY, 'de')
    expect(createGovueI18n().language.locale.value).toBe('de')
  })

  it('starts from the browser without a stored choice', () => {
    browser('en-GB', 'de')
    expect(createGovueI18n().language.locale.value).toBe('en')
  })

  it('starts German when the browser offers only French', () => {
    browser('fr-FR')
    expect(createGovueI18n().language.locale.value).toBe('de')
  })

  it('uses navigator.language when languages is empty', () => {
    vi.stubGlobal('navigator', { languages: [], language: 'en-US' })
    expect(createGovueI18n().language.locale.value).toBe('en')
  })

  it('ignores an unknown stored value', () => {
    localStorage.setItem(LANGUAGE_STORAGE_KEY, 'klingon')
    expect(createGovueI18n().language.locale.value).toBe('en')
  })

  it('does not throw when storage is blocked', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new DOMException('blocked', 'SecurityError')
    })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new DOMException('blocked', 'SecurityError')
    })
    const { language } = createGovueI18n()
    expect(language.locale.value).toBe('en')
    expect(() => language.setLocale('de')).not.toThrow()
    expect(language.locale.value).toBe('de')
    vi.restoreAllMocks()
  })

  it('sets lang on the document element, also when it changes', async () => {
    const { language } = createGovueI18n({ locale: 'de' })
    expect(document.documentElement.lang).toBe('de')
    language.setLocale('en')
    await Promise.resolve()
    expect(document.documentElement.lang).toBe('en')
  })

  it('stores a choice and ignores a code it does not know', () => {
    const { language } = createGovueI18n()
    language.setLocale('fr')
    expect(language.locale.value).toBe('en')
    expect(localStorage.getItem(LANGUAGE_STORAGE_KEY)).toBeNull()

    language.setLocale('de')
    expect(localStorage.getItem(LANGUAGE_STORAGE_KEY)).toBe('de')
  })

  it('does not store when persist is false', () => {
    const { language } = createGovueI18n()
    language.setLocale('de', { persist: false })
    expect(localStorage.getItem(LANGUAGE_STORAGE_KEY)).toBeNull()
  })

  it('stores under a custom localStorage key', () => {
    const { language } = createGovueI18n({ storage: { kind: 'local', key: 'my-lang' } })
    language.setLocale('de')
    expect(localStorage.getItem('my-lang')).toBe('de')
  })

  it('reads and writes through a callback for an account value', () => {
    let account: string | undefined = 'de'
    const set = vi.fn((code: string) => {
      account = code
    })
    const { language } = createGovueI18n({ storage: { kind: 'custom', get: () => account, set } })
    expect(language.locale.value).toBe('de')
    language.setLocale('en')
    expect(set).toHaveBeenCalledWith('en')
  })

  it('survives a throwing callback', () => {
    const { language } = createGovueI18n({
      storage: {
        kind: 'custom',
        get: () => {
          throw new Error('offline')
        },
        set: () => {
          throw new Error('offline')
        },
      },
    })
    expect(language.locale.value).toBe('en')
    expect(() => language.setLocale('de')).not.toThrow()
  })

  it('translates and fills placeholders', () => {
    const { language } = createGovueI18n({
      locale: 'de',
      messages: { de: { site: { hi: 'Hallo {name}' } }, en: { site: { hi: 'Hello {name}' } } },
    })
    expect(language.t('govue.error.requestId')).toBe('Anfrage-ID')
    expect(language.t('site.hi', { name: 'Ada' })).toBe('Hallo Ada')
    language.setLocale('en')
    expect(language.t('site.hi', { name: 'Ada' })).toBe('Hello Ada')
  })

  it('lists the languages by their own names', () => {
    expect(createGovueI18n().language.locales).toEqual([
      { code: 'de', name: 'Deutsch' },
      { code: 'en', name: 'English' },
    ])
  })

  it('merges the app messages and lets them override govue keys', () => {
    const { language } = createGovueI18n({
      locale: 'en',
      messages: {
        en: { site: { title: 'Home' }, govue: { skipLink: 'Jump to main' } },
        de: { site: { title: 'Start' } },
      },
    })
    expect(language.t('site.title')).toBe('Home')
    expect(language.t('govue.skipLink')).toBe('Jump to main')
    expect(language.t('govue.confirm.cancel')).toBe('Cancel')
  })

  it('lets the app add messages later through mergeLocaleMessage', () => {
    const { i18n, language } = createGovueI18n({ locale: 'en' })
    i18n.global.mergeLocaleMessage('en', { late: { key: 'Added' } })
    expect(language.t('late.key')).toBe('Added')
  })

  it('shares an existing vue-i18n instance and keeps its locale', () => {
    // Built like ostendorf.tech and calbridge build theirs, with no cast.
    const existing = createI18n({
      legacy: false,
      locale: 'en',
      fallbackLocale: 'de',
      missingWarn: true,
      fallbackWarn: false,
      messages: {
        de: { site: { hello: 'Hallo' } },
        en: { site: { hello: 'Hello' } },
      },
    })
    localStorage.setItem(LANGUAGE_STORAGE_KEY, 'de')

    const plugin = createGovueI18n({ i18n: existing })
    expect(plugin.i18n).toBe(existing)
    expect(plugin.language.locale.value).toBe('en')
    expect(existing.global.t('site.hello')).toBe('Hello')
    expect(existing.global.t('govue.confirm.cancel')).toBe('Cancel')

    // The app changes the locale itself, for example from a route.
    existing.global.locale.value = 'de'
    expect(plugin.language.locale.value).toBe('de')
    expect(plugin.language.t('govue.confirm.cancel')).toBe('Abbrechen')
  })

  it('applies detection to a shared instance when asked', () => {
    const existing = createI18n({
      legacy: false,
      locale: 'de',
      fallbackLocale: 'de',
      messages: { de: { site: { hello: 'Hallo' } }, en: { site: { hello: 'Hello' } } },
    })
    expect(createGovueI18n({ i18n: existing, detect: true }).language.locale.value).toBe('en')
  })

  it('falls back to German on a server: Node defines navigator but there is no window', () => {
    localStorage.setItem(LANGUAGE_STORAGE_KEY, 'en')
    browser('en-GB')
    vi.stubGlobal('window', undefined)
    const { language } = createGovueI18n()
    vi.unstubAllGlobals()
    expect(language.locale.value).toBe('de')
  })

  it('treats an unknown locale option like setLocale does', () => {
    const { language } = createGovueI18n({ locale: 'fr' })
    expect(language.locale.value).toBe('de')
    expect(document.documentElement.lang).toBe('de')
  })

  it('installs on an app and provides the language', () => {
    const app = createApp({ render: () => null })
    const plugin = createGovueI18n()
    expect(() => app.use(plugin)).not.toThrow()
  })
})

describe('createGovueI18n formality', () => {
  it('says du in German by default', () => {
    const { language } = createGovueI18n({ locale: 'de' })
    expect(language.t('govue.contact.sending')).toBe('Deine Nachricht wird gesendet')
    expect(language.t('govue.contact.error.required')).toBe('Bitte fülle dieses Feld aus.')
  })

  it('says Sie in German with the formal register, under the locale de', () => {
    const { language } = createGovueI18n({ locale: 'de', formality: 'formal' })
    expect(language.locale.value).toBe('de')
    expect(document.documentElement.lang).toBe('de')
    expect(language.t('govue.contact.sending')).toBe('Ihre Nachricht wird gesendet')
    expect(language.t('govue.contact.error.required')).toBe('Bitte füllen Sie dieses Feld aus.')
    expect(language.t('govue.contact.error.rateLimitedWait', { wait: 'in etwa 5 Minuten' })).toBe(
      'Zu viele Nachrichten. Bitte versuchen Sie es in etwa 5 Minuten erneut.',
    )
    // A neutral text is the same in both registers.
    expect(language.t('govue.contact.submit')).toBe('Nachricht senden')
  })

  it('is no second German in the language list', () => {
    expect(createGovueI18n({ formality: 'formal' }).language.locales).toEqual([
      { code: 'de', name: 'Deutsch' },
      { code: 'en', name: 'English' },
    ])
  })

  it('keeps English as it is', () => {
    const { language } = createGovueI18n({ locale: 'en', formality: 'formal' })
    expect(language.t('govue.contact.sending')).toBe(
      createGovueI18n({ locale: 'en' }).language.t('govue.contact.sending'),
    )
  })

  it('stays formal after a switch to English and back', () => {
    const { language } = createGovueI18n({ locale: 'de', formality: 'formal' })
    language.setLocale('en', { persist: false })
    language.setLocale('de', { persist: false })
    expect(language.t('govue.contact.success')).toBe('Danke. Ihre Nachricht wurde gesendet.')
  })

  it('lets the app messages override a formal text', () => {
    const { language } = createGovueI18n({
      locale: 'de',
      formality: 'formal',
      messages: { de: { govue: { contact: { success: 'Vielen Dank.' } } } },
    })
    expect(language.t('govue.contact.success')).toBe('Vielen Dank.')
    expect(language.t('govue.contact.sending')).toBe('Ihre Nachricht wird gesendet')
  })

  it('loads the formal register into a shared instance', () => {
    const existing = createI18n({ legacy: false, locale: 'de', messages: { de: {}, en: {} } })
    createGovueI18n({ i18n: existing, formality: 'formal' })
    expect(existing.global.t('govue.contact.sending')).toBe('Ihre Nachricht wird gesendet')
  })

  it('refuses an unknown register', () => {
    expect(() => createGovueI18n({ formality: 'polite' as unknown as 'formal' })).toThrow(
      /formality/,
    )
  })
})
