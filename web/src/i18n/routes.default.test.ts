// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it, vi } from 'vitest'

// The default language is the second one here, so an x-default that points at
// the first language instead of the default is caught.
vi.mock('./catalogues', async (importOriginal) => ({
  ...(await importOriginal<Record<string, unknown>>()),
  DEFAULT_LOCALE: 'en',
}))

import { localeCodes } from './catalogues'
import { hreflangLinks, sitemapXml } from './routes'

describe('x-default follows DEFAULT_LOCALE', () => {
  it('is not the first language in this setup', () => {
    expect(localeCodes[0]).not.toBe('en')
  })

  it('points the hreflang x-default at the default language variant', () => {
    const out = hreflangLinks(
      { source: '/impressum', locale: 'de', path: '/de/impressum' },
      'https://example.com',
    )
    expect(out).toContain('hreflang="x-default" href="https://example.com/en/impressum"')
  })

  it('points the sitemap x-default at the default language variant', () => {
    const xml = sitemapXml([{ path: '/impressum', public: true }], 'https://example.com')
    expect(xml).toContain('hreflang="x-default" href="https://example.com/en/impressum"')
    expect(xml).not.toContain('hreflang="x-default" href="https://example.com/de/impressum"')
  })
})
