// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { render, screen } from '@testing-library/vue'
import { describe, expect, it, vi } from 'vitest'
import { computed } from 'vue'

import GovueLanguageSwitch from './GovueLanguageSwitch.vue'

// The catalogues list two languages, so a third one comes from a stand in for
// the language layer. othersOnly is written for any number of languages.
vi.mock('../i18n/plugin', () => ({
  useLanguage: () => ({
    locale: computed(() => 'en'),
    locales: [
      { code: 'de', name: 'Deutsch' },
      { code: 'en', name: 'English' },
      { code: 'fr', name: 'Français' },
    ],
    setLocale: () => {},
  }),
  useGovueText: () => ({ t: (key: string) => key }),
}))

describe('GovueLanguageSwitch othersOnly with three languages', () => {
  it('renders the two other languages and leaves the current one out', () => {
    render(GovueLanguageSwitch, {
      props: { linkFor: (code: string) => `/${code}/about`, othersOnly: true },
    })

    const links = screen.getAllByRole('link')
    expect(links.map((l) => l.getAttribute('hreflang'))).toEqual(['de', 'fr'])
    expect(links.map((l) => l.getAttribute('lang'))).toEqual(['de', 'fr'])
    expect(links.map((l) => l.getAttribute('href'))).toEqual(['/de/about', '/fr/about'])
    expect(screen.queryByRole('link', { name: 'English' })).not.toBeInTheDocument()
  })
})
