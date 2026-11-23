// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import userEvent from '@testing-library/user-event'
import { render, screen } from '@testing-library/vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { LANGUAGE_STORAGE_KEY } from '../i18n/languageStorage'
import { createGovueI18n, type GovueI18nOptions } from '../i18n/plugin'
import GovueLanguageSwitch from './GovueLanguageSwitch.vue'

function mount(options: GovueI18nOptions = {}, props: Record<string, unknown> = {}) {
  return render(GovueLanguageSwitch, {
    props,
    global: { plugins: [createGovueI18n(options)] },
  })
}

/** fireClickPrevented dispatches a cancelable click and says whether a handler
 *  cancelled the browser's own navigation. jsdom does not navigate, so this is
 *  the observable. */
async function fireClickPrevented(el: Element, init: MouseEventInit = {}): Promise<boolean> {
  const event = new MouseEvent('click', { bubbles: true, cancelable: true, ...init })
  el.dispatchEvent(event)
  await Promise.resolve()
  return event.defaultPrevented
}

describe('GovueLanguageSwitch', () => {
  beforeEach(() => {
    vi.unstubAllGlobals()
    vi.stubGlobal('navigator', { languages: ['en-US'], language: 'en-US' })
    localStorage.clear()
    document.documentElement.removeAttribute('lang')
  })

  it('is a named select with each language in its own language', () => {
    mount()

    const select = screen.getByRole('combobox', { name: 'Language' })
    expect(select).toBeVisible()
    const options = screen.getAllByRole('option')
    expect(options.map((o) => o.textContent?.trim())).toEqual(['Deutsch', 'English'])
    expect(options.map((o) => o.getAttribute('lang'))).toEqual(['de', 'en'])
  })

  it('has its name in the active language', () => {
    mount({ locale: 'de' })

    expect(screen.getByRole('combobox', { name: 'Sprache' })).toBeVisible()
  })

  it('shows the active language as selected', () => {
    mount({ locale: 'de' })

    expect(screen.getByRole('option', { name: 'Deutsch' })).toHaveProperty('selected', true)
  })

  it('switches, stores the choice and sets lang on the document', async () => {
    const user = userEvent.setup()
    const view = mount()

    await user.selectOptions(screen.getByRole('combobox'), 'de')

    expect(localStorage.getItem(LANGUAGE_STORAGE_KEY)).toBe('de')
    expect(document.documentElement.lang).toBe('de')
    expect(screen.getByRole('combobox', { name: 'Sprache' })).toBeVisible()
    expect(view.emitted('change')).toEqual([['de']])
  })

  it('keeps focus on the control after switching', async () => {
    const user = userEvent.setup()
    mount()
    const select = screen.getByRole('combobox')

    await user.tab()
    expect(select).toHaveFocus()
    await user.selectOptions(select, 'de')

    expect(screen.getByRole('combobox')).toHaveFocus()
  })

  it('is one stop in the tab order and reachable by keyboard', async () => {
    const user = userEvent.setup()
    mount()

    await user.tab()
    expect(screen.getByRole('combobox')).toHaveFocus()
  })

  it('does not navigate or reload', async () => {
    const user = userEvent.setup()
    const before = window.location.href
    mount()

    await user.selectOptions(screen.getByRole('combobox'), 'de')

    expect(window.location.href).toBe(before)
  })

  it('takes its own label', () => {
    mount({}, { label: 'Pick a language' })

    expect(screen.getByRole('combobox', { name: 'Pick a language' })).toBeVisible()
  })

  it('has a 44px touch target', () => {
    mount()

    expect(screen.getByRole('combobox')).toHaveClass('min-h-11')
  })

  describe('link mode', () => {
    const linkFor = (code: string) => `/${code}/about`

    // jsdom cannot leave the document, so the tests that let a click through to
    // the browser use hash targets, which it can follow.
    const hashFor = (code: string) => `#/${code}/about`
    afterEach(() => {
      window.location.hash = ''
    })

    it('renders each language as a link to the same page with hreflang and lang', () => {
      mount({}, { linkFor })

      const de = screen.getByRole('link', { name: 'Deutsch' })
      const en = screen.getByRole('link', { name: 'English' })
      expect(de).toHaveAttribute('href', '/de/about')
      expect(de).toHaveAttribute('hreflang', 'de')
      expect(de).toHaveAttribute('lang', 'de')
      expect(en).toHaveAttribute('href', '/en/about')
      expect(en).toHaveAttribute('hreflang', 'en')
      expect(en).toHaveAttribute('lang', 'en')
    })

    it('marks the current language with aria-current and not the other', () => {
      mount({ locale: 'de' }, { linkFor })

      expect(screen.getByRole('link', { name: 'Deutsch' })).toHaveAttribute('aria-current', 'true')
      expect(screen.getByRole('link', { name: 'English' })).not.toHaveAttribute('aria-current')
    })

    it('is a named group of links, not a select and not radios', () => {
      mount({}, { linkFor })

      expect(screen.getByRole('group', { name: 'Language' })).toBeVisible()
      expect(screen.queryByRole('combobox')).not.toBeInTheDocument()
      expect(screen.queryByRole('radio')).not.toBeInTheDocument()
      expect(screen.getAllByRole('link')).toHaveLength(2)
    })

    it('names the group in the active language, or by its own label', () => {
      mount({ locale: 'de' }, { linkFor })
      expect(screen.getByRole('group', { name: 'Sprache' })).toBeVisible()
    })

    it('takes its own label', () => {
      mount({}, { linkFor, label: 'Pick a language' })

      expect(screen.getByRole('group', { name: 'Pick a language' })).toBeVisible()
    })

    it('has 44px targets and role tokens only', () => {
      mount({}, { linkFor })

      for (const link of screen.getAllByRole('link')) {
        expect(link).toHaveClass('min-h-11', 'min-w-11')
        expect(link.className).not.toMatch(/#[0-9a-f]{3,6}|rgb|outline|ring/i)
      }
    })

    it('is reachable by keyboard, each link one stop, and Enter follows it', async () => {
      const user = userEvent.setup()
      const navigate = vi.fn(() => true)
      mount({}, { linkFor, navigate })

      await user.tab()
      expect(screen.getByRole('link', { name: 'Deutsch' })).toHaveFocus()
      await user.tab()
      expect(screen.getByRole('link', { name: 'English' })).toHaveFocus()

      await user.tab({ shift: true })
      await user.keyboard('{Enter}')

      expect(navigate).toHaveBeenCalledWith('/de/about', 'de')
      expect(document.documentElement.lang).toBe('de')
    })

    it('updates the locale without a page load when the router takes the click', async () => {
      const user = userEvent.setup()
      const navigate = vi.fn(() => true)
      const view = mount({}, { linkFor, navigate })
      const de = screen.getByRole('link', { name: 'Deutsch' })

      // The router has the path, so the browser's own navigation is cancelled.
      expect(await fireClickPrevented(de)).toBe(true)
      await user.click(de)

      expect(navigate).toHaveBeenCalledWith('/de/about', 'de')
      expect(localStorage.getItem(LANGUAGE_STORAGE_KEY)).toBe('de')
      expect(document.documentElement.lang).toBe('de')
      expect(screen.getByRole('group', { name: 'Sprache' })).toBeVisible()
      expect(screen.getByRole('link', { name: 'Deutsch' })).toHaveAttribute('aria-current', 'true')
      expect(view.emitted('change')?.[0]).toEqual(['de'])
    })

    it('lets the browser navigate when no router takes the click', async () => {
      const user = userEvent.setup()
      const navigate = vi.fn(() => false)
      mount({}, { linkFor: hashFor, navigate })

      expect(await fireClickPrevented(screen.getByRole('link', { name: 'Deutsch' }))).toBe(false)
      await user.click(screen.getByRole('link', { name: 'Deutsch' }))
      expect(navigate).toHaveBeenCalledWith('#/de/about', 'de')
      // The browser followed the link.
      expect(window.location.hash).toBe('#/de/about')
      // The choice is kept for the page the browser loads next.
      expect(localStorage.getItem(LANGUAGE_STORAGE_KEY)).toBe('de')
    })

    it('navigates plainly, and still keeps the choice, without a navigate prop', async () => {
      const user = userEvent.setup()
      mount({}, { linkFor: hashFor })
      const de = screen.getByRole('link', { name: 'Deutsch' })

      expect(await fireClickPrevented(de)).toBe(false)
      await user.click(de)

      expect(localStorage.getItem(LANGUAGE_STORAGE_KEY)).toBe('de')
    })

    it('leaves a click with a modifier to the browser and changes nothing', async () => {
      const navigate = vi.fn(() => true)
      mount({}, { linkFor: hashFor, navigate })
      const de = screen.getByRole('link', { name: 'Deutsch' })

      for (const init of [
        { ctrlKey: true },
        { metaKey: true },
        { shiftKey: true },
        { button: 1 },
      ]) {
        expect(await fireClickPrevented(de, init)).toBe(false)
      }

      expect(navigate).not.toHaveBeenCalled()
      expect(localStorage.getItem(LANGUAGE_STORAGE_KEY)).toBeNull()
    })
  })

  it('renders no link and no group without linkFor', () => {
    mount()

    expect(screen.queryByRole('link')).not.toBeInTheDocument()
    expect(screen.queryByRole('group')).not.toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: 'Language' })).toBeVisible()
  })

  it('needs the plugin', () => {
    const error = vi.spyOn(console, 'error').mockImplementation(() => {})
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    expect(() => render(GovueLanguageSwitch)).toThrow(/createGovueI18n/)
    error.mockRestore()
    warn.mockRestore()
  })
})
