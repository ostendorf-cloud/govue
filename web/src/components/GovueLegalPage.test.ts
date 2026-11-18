// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { render, screen } from '@testing-library/vue'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { LegalPage } from '../i18n/legal'
import { createGovueI18n } from '../i18n/plugin'
import GovueLegalPage from './GovueLegalPage.vue'

function withLocale(locale: string) {
  return { global: { plugins: [createGovueI18n({ locale })] } }
}

const siteText = '<h2>Provider</h2><p>Site text.</p>'

beforeEach(() => {
  localStorage.clear()
})

describe('GovueLegalPage', () => {
  describe.each<[string, LegalPage, string, string, string]>([
    ['de', 'imprint', 'Impressum', 'Zurück zur Startseite', '/de/'],
    ['de', 'privacy', 'Datenschutzerklärung', 'Zurück zur Startseite', '/de/'],
    ['de', 'terms', 'Allgemeine Geschäftsbedingungen', 'Zurück zur Startseite', '/de/'],
    ['en', 'imprint', 'Imprint', 'Back to the start page', '/en/'],
    ['en', 'privacy', 'Privacy policy', 'Back to the start page', '/en/'],
    ['en', 'terms', 'Terms and conditions', 'Back to the start page', '/en/'],
  ])('%s %s', (locale, page, title, back, home) => {
    it('has one h1 from the catalogue, the back link and the main landmark', () => {
      render(GovueLegalPage, {
        ...withLocale(locale),
        props: { page },
        slots: { default: siteText },
      })
      const headings = screen.getAllByRole('heading', { level: 1 })
      expect(headings).toHaveLength(1)
      expect(headings[0]).toHaveTextContent(title)
      expect(screen.getByRole('link', { name: back })).toHaveAttribute('href', home)
      expect(screen.getByRole('main')).toHaveAttribute('id', 'main')
    })
  })

  it('puts the site text after the h1, with its h2 headings', () => {
    render(GovueLegalPage, {
      ...withLocale('en'),
      props: { page: 'imprint' },
      slots: { default: siteText },
    })
    const h1 = screen.getByRole('heading', { level: 1 })
    const h2 = screen.getByRole('heading', { level: 2, name: 'Provider' })
    expect(Boolean(h1.compareDocumentPosition(h2) & Node.DOCUMENT_POSITION_FOLLOWING)).toBe(true)
  })

  it('ships no text of its own besides the title and the links', () => {
    const { container } = render(GovueLegalPage, {
      ...withLocale('en'),
      props: { page: 'privacy' },
    })
    const main = container.querySelector('main')
    expect(main?.querySelectorAll('p, h2, section')).toHaveLength(0)
  })

  it('has a skip link to the main landmark, and can leave it out', () => {
    render(GovueLegalPage, { ...withLocale('en'), props: { page: 'imprint' } })
    expect(screen.getByRole('link', { name: 'Skip to content' })).toHaveAttribute('href', '#main')
  })

  it('leaves the skip link out on request', () => {
    render(GovueLegalPage, { ...withLocale('en'), props: { page: 'imprint', skipLink: false } })
    expect(screen.queryByRole('link', { name: 'Skip to content' })).toBeNull()
  })

  it('takes the title and the back link as overrides', () => {
    render(GovueLegalPage, {
      ...withLocale('de'),
      props: {
        page: 'imprint',
        title: 'Anbieterkennzeichnung',
        backHref: '/de/app',
        backLabel: 'Zur App',
      },
    })
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('Anbieterkennzeichnung')
    expect(screen.getByRole('link', { name: 'Zur App' })).toHaveAttribute('href', '/de/app')
  })

  it('goes to the page of the chosen language', async () => {
    const navigate = vi.fn()
    render(GovueLegalPage, { ...withLocale('de'), props: { page: 'privacy', navigate } })
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Sprache' }), 'en')
    expect(navigate).toHaveBeenCalledWith('/en/privacy')
  })

  it('hides the language switch on request', () => {
    render(GovueLegalPage, {
      ...withLocale('de'),
      props: { page: 'privacy', languageSwitch: false },
    })
    expect(screen.queryByRole('combobox')).toBeNull()
  })

  it('renders in English without the language layer and without a switch', () => {
    render(GovueLegalPage, { props: { page: 'terms' } })
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('Terms and conditions')
    expect(screen.getByRole('link', { name: 'Back to the start page' })).toHaveAttribute(
      'href',
      '/en/',
    )
    expect(screen.queryByRole('combobox')).toBeNull()
  })
})
