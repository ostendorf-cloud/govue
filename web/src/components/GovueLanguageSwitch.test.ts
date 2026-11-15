// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import userEvent from '@testing-library/user-event'
import { render, screen } from '@testing-library/vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { LANGUAGE_STORAGE_KEY } from '../i18n/languageStorage'
import { createGovueI18n, type GovueI18nOptions } from '../i18n/plugin'
import GovueLanguageSwitch from './GovueLanguageSwitch.vue'

function mount(options: GovueI18nOptions = {}, props: Record<string, unknown> = {}) {
  return render(GovueLanguageSwitch, {
    props,
    global: { plugins: [createGovueI18n(options)] },
  })
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

  it('needs the plugin', () => {
    const error = vi.spyOn(console, 'error').mockImplementation(() => {})
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    expect(() => render(GovueLanguageSwitch)).toThrow(/createGovueI18n/)
    error.mockRestore()
    warn.mockRestore()
  })
})
