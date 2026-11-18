// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'

import { catalogues, localeCodes, type LocaleCode } from './catalogues'
import {
  LEGAL_PAGES,
  legalLinks,
  legalPath,
  legalRouteDefs,
  legalRoutes,
  type LegalPage,
} from './legal'
import type { MessageTree } from './messages'
import { localizeRoutes, validateRoutes } from './routes'

function text(code: LocaleCode, key: string): string {
  let node: string | MessageTree | undefined = catalogues[code].messages
  for (const part of key.split('.')) {
    node = typeof node === 'object' ? node[part] : undefined
  }
  if (typeof node !== 'string') throw new Error(`no text ${code} ${key}`)
  return node
}

describe('legalPath', () => {
  it('builds the canonical path of every page and language', () => {
    expect(legalPath('imprint', 'de')).toBe('/de/imprint')
    expect(legalPath('privacy', 'en')).toBe('/en/privacy')
    expect(legalPath('terms', 'de')).toBe('/de/terms')
  })

  it('throws for a page or language outside the fixed lists', () => {
    expect(() => legalPath('//evil.example' as LegalPage, 'de')).toThrow(/unknown page/)
    expect(() => legalPath('imprint', 'fr' as LocaleCode)).toThrow(/unknown language/)
    expect(() => legalPath('imprint', '/evil' as LocaleCode)).toThrow(/unknown language/)
  })
})

describe('legalRouteDefs', () => {
  it('gives imprint, privacy and terms as public routes', () => {
    expect(legalRouteDefs({ terms: {} })).toEqual([
      { path: '/imprint', public: true },
      { path: '/privacy', public: true },
      { path: '/terms', public: true },
    ])
  })

  it('leaves terms out when the site has none', () => {
    expect(legalRouteDefs({}).map((r) => r.path)).toEqual(['/imprint', '/privacy'])
  })

  it('localizes to the canonical paths of the Go redirects', () => {
    const routes = legalRouteDefs({ terms: {} })
    validateRoutes(routes)
    expect(localizeRoutes(routes).map((v) => v.path)).toEqual([
      '/de/imprint',
      '/en/imprint',
      '/de/privacy',
      '/en/privacy',
      '/de/terms',
      '/en/terms',
    ])
  })
})

describe('legalRoutes', () => {
  const imprint = { name: 'Imprint' }
  const privacy = { name: 'Privacy' }
  const terms = { name: 'Terms' }

  it('gives one router record per page and language', () => {
    const records = legalRoutes({ imprint, privacy, terms })
    expect(records).toHaveLength(LEGAL_PAGES.length * localeCodes.length)
    expect(records[0]).toEqual({
      path: '/de/imprint',
      name: 'govue-legal-imprint-de',
      component: imprint,
      meta: { locale: 'de', legalPage: 'imprint' },
    })
    expect(records.find((r) => r.path === '/en/terms')?.component).toBe(terms)
    expect(records.find((r) => r.path === '/en/privacy')?.meta).toEqual({
      locale: 'en',
      legalPage: 'privacy',
    })
    expect(new Set(records.map((r) => r.name)).size).toBe(records.length)
  })

  it('leaves terms out when the site has none', () => {
    const records = legalRoutes({ imprint, privacy })
    expect(records.map((r) => r.path)).toEqual([
      '/de/imprint',
      '/en/imprint',
      '/de/privacy',
      '/en/privacy',
    ])
  })

  it('requires imprint and privacy', () => {
    expect(() => legalRoutes({ imprint, privacy: undefined as unknown as object })).toThrow(
      /privacy page is required/,
    )
    expect(() => legalRoutes({ imprint: null as unknown as object, privacy })).toThrow(
      /imprint page is required/,
    )
  })
})

describe('legalLinks', () => {
  for (const code of localeCodes) {
    it(`links the pages in ${code} with the catalogue labels`, () => {
      const links = legalLinks(code, ['terms', 'imprint', 'privacy'], (key) => text(code, key))
      expect(links).toEqual([
        {
          page: 'imprint',
          href: `/${code}/imprint`,
          label: text(code, 'govue.legal.link.imprint'),
        },
        {
          page: 'privacy',
          href: `/${code}/privacy`,
          label: text(code, 'govue.legal.link.privacy'),
        },
        { page: 'terms', href: `/${code}/terms`, label: text(code, 'govue.legal.link.terms') },
      ])
    })
  }

  it('links only the pages asked for', () => {
    expect(legalLinks('en', ['imprint'], (k) => k).map((l) => l.href)).toEqual(['/en/imprint'])
  })
})

describe('legal catalogue texts', () => {
  it('names every page in both languages', () => {
    expect(text('de', 'govue.legal.link.imprint')).toBe('Impressum')
    expect(text('de', 'govue.legal.link.privacy')).toBe('Datenschutz')
    expect(text('de', 'govue.legal.link.terms')).toBe('AGB')
    expect(text('en', 'govue.legal.link.imprint')).toBe('Imprint')
    expect(text('en', 'govue.legal.link.privacy')).toBe('Privacy')
    expect(text('en', 'govue.legal.link.terms')).toBe('Terms')
    for (const code of localeCodes) {
      for (const page of LEGAL_PAGES) {
        expect(text(code, `govue.legal.title.${page}`)).not.toBe('')
      }
      expect(text(code, 'govue.legal.back')).not.toBe('')
    }
  })
})
