// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { render, screen } from '@testing-library/vue'
import { beforeEach, describe, expect, it } from 'vitest'
import { defineComponent, h, nextTick } from 'vue'

import { createGovueI18n } from '../i18n/plugin'
import { resetThemeForTests } from '../composables/useTheme'
import { useToasts } from '../composables/useToasts'
import GovueButton from './GovueButton.vue'
import GovueConfirmDialog from './GovueConfirmDialog.vue'
import GovueEmptyState from './GovueEmptyState.vue'
import GovueErrorState from './GovueErrorState.vue'
import GovueField from './GovueField.vue'
import GovueFooter from './GovueFooter.vue'
import GovueNav from './GovueNav.vue'
import GovueSkipLink from './GovueSkipLink.vue'
import GovueThemeSwitch from './GovueThemeSwitch.vue'
import GovueToastHost from './GovueToastHost.vue'

// What a component says when the app passes no text, in each language and
// with no language layer at all.

function withLocale(locale: string) {
  return { global: { plugins: [createGovueI18n({ locale })] } }
}

beforeEach(() => {
  useToasts().dismissAll()
  resetThemeForTests()
  localStorage.clear()
})

describe('default texts', () => {
  describe.each([
    ['de', 'Zum Inhalt springen'],
    ['en', 'Skip to content'],
  ])('skip link in %s', (locale, text) => {
    it('reads from the catalogue', () => {
      render(GovueSkipLink, withLocale(locale))
      expect(screen.getByRole('link', { name: text })).toBeVisible()
    })
  })

  it('skip link keeps its slot override', () => {
    render(GovueSkipLink, { ...withLocale('de'), slots: { default: 'Own text' } })
    expect(screen.getByRole('link', { name: 'Own text' })).toBeVisible()
  })

  describe.each([
    ['de', 'Löschen', 'Abbrechen'],
    ['en', 'Delete', 'Cancel'],
  ])('confirm dialog in %s', (locale, confirm, cancel) => {
    it('has its buttons from the catalogue', async () => {
      render(GovueConfirmDialog, {
        ...withLocale(locale),
        props: { open: true, title: 'T', message: 'M', subject: 'S' },
      })
      expect(await screen.findByRole('button', { name: confirm })).toBeVisible()
      expect(screen.getByRole('button', { name: cancel })).toBeVisible()
    })
  })

  it('confirm dialog props still override the catalogue', async () => {
    render(GovueConfirmDialog, {
      ...withLocale('de'),
      props: {
        open: true,
        title: 'T',
        message: 'M',
        subject: 'S',
        confirmLabel: 'Remove',
        cancelLabel: 'Keep',
      },
    })
    expect(await screen.findByRole('button', { name: 'Remove' })).toBeVisible()
    expect(screen.getByRole('button', { name: 'Keep' })).toBeVisible()
  })

  describe.each([
    ['de', 'Noch nichts vorhanden'],
    ['en', 'Nothing here yet'],
  ])('empty state in %s', (locale, title) => {
    it('has a default title', () => {
      render(GovueEmptyState, withLocale(locale))
      expect(screen.getByRole('heading', { name: title })).toBeVisible()
    })
  })

  describe.each([
    ['de', 'Etwas ist schiefgegangen', 'Erneut versuchen', 'Anfrage-ID'],
    ['en', 'Something went wrong', 'Try again', 'Request id'],
  ])('error state in %s', (locale, title, retry, requestId) => {
    it('has default texts', () => {
      render(GovueErrorState, { ...withLocale(locale), props: { requestId: 'abc' } })
      expect(screen.getByRole('heading', { name: title })).toBeVisible()
      expect(screen.getByRole('button', { name: retry })).toBeVisible()
      expect(screen.getByText(new RegExp(requestId))).toBeVisible()
      expect(screen.getByText('abc').tagName).toBe('CODE')
    })
  })

  describe.each([
    ['de', 'Darstellung', ['System', 'Hell', 'Dunkel']],
    ['en', 'Theme', ['System', 'Light', 'Dark']],
  ])('theme switch in %s', (locale, label, options) => {
    it('has its labels from the catalogue', () => {
      render(GovueThemeSwitch, withLocale(locale))
      expect(screen.getByRole('radiogroup', { name: label })).toBeVisible()
      expect(screen.getAllByRole('radio').map((r) => r.textContent?.trim())).toEqual(options)
    })
  })

  describe.each([
    ['de', 'Darstellung', ['Dem System folgen', 'Heller Modus', 'Dunkler Modus']],
    ['en', 'Theme', ['Follow the system', 'Light mode', 'Dark mode']],
  ])('compact theme switch in %s', (locale, group, names) => {
    it('has its accessible names from the catalogue', () => {
      render(GovueThemeSwitch, { ...withLocale(locale), props: { compact: true } })
      expect(screen.getByRole('radiogroup', { name: group })).toBeVisible()
      expect(screen.getAllByRole('radio').map((r) => r.getAttribute('aria-label'))).toEqual(names)
    })
  })

  it('theme switch props still override the catalogue', () => {
    render(GovueThemeSwitch, { ...withLocale('de'), props: { label: 'Look', darkLabel: 'Night' } })
    expect(screen.getByRole('radiogroup', { name: 'Look' })).toBeVisible()
    expect(screen.getByRole('radio', { name: 'Night' })).toBeVisible()
  })

  describe.each([
    ['de', 'Hauptmenü', 'Menü', 'Fußbereich', '(Pflichtfeld)'],
    ['en', 'Main', 'Menu', 'Footer', '(required)'],
  ])('layout and form in %s', (locale, nav, menu, footer, required) => {
    it('names navigation, menu button, footer and required marker', () => {
      render(
        defineComponent({
          setup: () => () =>
            h('div', [
              h(GovueNav, { items: [{ href: '/', label: 'A' }] }),
              h(GovueFooter, { links: [{ href: '/i', label: 'I' }] }),
              h(GovueField, { label: 'Name', required: true }),
            ]),
        }),
        withLocale(locale),
      )
      expect(screen.getByRole('navigation', { name: nav })).toBeInTheDocument()
      expect(screen.getByRole('button', { name: menu })).toBeInTheDocument()
      expect(screen.getByRole('navigation', { name: footer })).toBeInTheDocument()
      expect(screen.getByText(required)).toBeInTheDocument()
    })
  })

  describe.each([
    ['de', 'Wird ausgeführt'],
    ['en', 'Working'],
  ])('button in %s', (locale, text) => {
    it('has a loading text', () => {
      render(GovueButton, { ...withLocale(locale), props: { loading: true } })
      expect(screen.getByText(text)).toBeInTheDocument()
    })
  })

  describe.each([
    ['de', 'Fehler', 'Schließen'],
    ['en', 'Error', 'Dismiss'],
  ])('toast host in %s', (locale, kind, dismiss) => {
    it('names the kind and the dismiss button', async () => {
      render(GovueToastHost, withLocale(locale))
      useToasts().notify({ kind: 'error', title: 'Boom' })
      await nextTick()
      expect(screen.getByText(kind)).toBeVisible()
      expect(screen.getByRole('button', { name: dismiss })).toBeVisible()
    })
  })

  describe('without the language layer', () => {
    it('stays English, as before the layer existed', () => {
      render(GovueSkipLink)
      expect(screen.getByRole('link', { name: 'Skip to content' })).toBeVisible()
    })
  })
})
