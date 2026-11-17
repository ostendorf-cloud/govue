// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'

import { localeCodes } from './catalogues'
import {
  hreflangLinks,
  localeFromPath,
  localizedPath,
  localizeRoutes,
  sitemapXml,
  validateOrigin,
  validateRoutes,
} from './routes'

const ORIGIN = 'https://example.com'

describe('validateRoutes', () => {
  const bad: [string, string][] = [
    ['empty', ''],
    ['no leading slash', 'impressum'],
    ['double slash', '/a//b'],
    ['leading double slash', '//evil.example.com'],
    ['dot segment', '/a/./b'],
    ['dot dot segment', '/a/../b'],
    ['query', '/a?x=1'],
    ['fragment', '/a#top'],
    ['trailing slash', '/impressum/'],
    ['backslash', '/a\\b'],
    ['space', '/a b'],
    ['control character', '/a\u0000b'],
    ['language prefix', '/de/impressum'],
    ['bare language prefix', '/en'],
  ]
  it.each(bad)('rejects %s', (_name, path) => {
    expect(() => validateRoutes([{ path }])).toThrow(/route/i)
  })

  it('names the trailing slash, not an empty segment', () => {
    expect(() => validateRoutes([{ path: '/impressum/' }])).toThrow(/end with/)
  })

  it('rejects a duplicate path', () => {
    expect(() => validateRoutes([{ path: '/a' }, { path: '/a', public: true }])).toThrow(
      /duplicate/i,
    )
  })

  it('accepts the root, a nested path and a non ASCII path', () => {
    expect(() =>
      validateRoutes([{ path: '/', public: true }, { path: '/a/b' }, { path: '/über' }]),
    ).not.toThrow()
  })

  it('does not treat a longer first segment as a language prefix', () => {
    expect(() => validateRoutes([{ path: '/deutsch' }, { path: '/english' }])).not.toThrow()
  })

  it('rejects a non boolean public flag', () => {
    expect(() => validateRoutes([{ path: '/a', public: 'yes' as unknown as boolean }])).toThrow(
      /public/,
    )
  })
})

describe('validateOrigin', () => {
  it('accepts https with and without a port, and http for localhost', () => {
    expect(validateOrigin('https://example.com')).toBe('https://example.com')
    expect(validateOrigin('https://example.com:8443')).toBe('https://example.com:8443')
    expect(validateOrigin('http://localhost:5173')).toBe('http://localhost:5173')
  })

  const bad: [string, string][] = [
    ['empty', ''],
    ['no scheme', 'example.com'],
    ['http for another host', 'http://example.com'],
    ['a path', 'https://example.com/app'],
    ['a trailing slash', 'https://example.com/'],
    ['a query', 'https://example.com?a=1'],
    ['a fragment', 'https://example.com#a'],
    ['userinfo', 'https://user:pw@example.com'],
    ['another scheme', 'ftp://example.com'],
    ['upper case host', 'https://Example.com'],
  ]
  it.each(bad)('rejects %s', (_name, origin) => {
    expect(() => validateOrigin(origin)).toThrow(/origin/i)
  })
})

describe('localizedPath', () => {
  it('prefixes a path with the language', () => {
    expect(localizedPath('/impressum', 'de')).toBe('/de/impressum')
    expect(localizedPath('/a/b', 'en')).toBe('/en/a/b')
  })

  it('maps the root to the language root with a trailing slash', () => {
    expect(localizedPath('/', 'en')).toBe('/en/')
  })

  it('swaps an existing prefix instead of stacking one', () => {
    expect(localizedPath('/en/impressum', 'de')).toBe('/de/impressum')
    expect(localizedPath('/en/', 'de')).toBe('/de/')
    expect(localizedPath('/en', 'de')).toBe('/de/')
  })

  it('does not strip a first segment that only starts like a language', () => {
    expect(localizedPath('/deutsch', 'en')).toBe('/en/deutsch')
  })

  it('throws for an unknown language and for an invalid path', () => {
    expect(() => localizedPath('/a', 'fr' as never)).toThrow(/language/i)
    expect(() => localizedPath('//evil.example.com', 'de')).toThrow(/route/i)
    expect(() => localizedPath('/a/../b', 'de')).toThrow(/route/i)
  })
})

describe('localeFromPath', () => {
  it('reads the language of a prefixed path', () => {
    expect(localeFromPath('/en/')).toBe('en')
    expect(localeFromPath('/en')).toBe('en')
    expect(localeFromPath('/de/impressum')).toBe('de')
  })

  it('returns undefined without a prefix', () => {
    expect(localeFromPath('/')).toBeUndefined()
    expect(localeFromPath('/impressum')).toBeUndefined()
    expect(localeFromPath('/deutsch')).toBeUndefined()
    expect(localeFromPath('')).toBeUndefined()
  })

  it('matches the exact code only', () => {
    expect(localeFromPath('/EN/')).toBeUndefined()
    expect(localeFromPath('/fr/')).toBeUndefined()
  })
})

describe('localizeRoutes', () => {
  it('returns one variant per language for every public route', () => {
    const out = localizeRoutes([
      { path: '/', public: true },
      { path: '/impressum', public: true },
    ])
    expect(out).toEqual([
      { source: '/', locale: 'de', path: '/de/' },
      { source: '/', locale: 'en', path: '/en/' },
      { source: '/impressum', locale: 'de', path: '/de/impressum' },
      { source: '/impressum', locale: 'en', path: '/en/impressum' },
    ])
  })

  it('leaves out app routes and routes without the flag', () => {
    const out = localizeRoutes([{ path: '/app' }, { path: '/x', public: false }])
    expect(out).toEqual([])
  })

  it('validates the table', () => {
    expect(() => localizeRoutes([{ path: '/a/', public: true }])).toThrow(/route/i)
  })

  it('covers every configured language', () => {
    const out = localizeRoutes([{ path: '/a', public: true }])
    expect(out.map((v) => v.locale)).toEqual(localeCodes)
  })
})

describe('hreflangLinks', () => {
  const variant = { source: '/impressum', locale: 'en', path: '/en/impressum' } as const

  it('emits the canonical, every language and x-default', () => {
    expect(hreflangLinks(variant, ORIGIN)).toBe(
      [
        '<link rel="canonical" href="https://example.com/en/impressum">',
        '<link rel="alternate" hreflang="de" href="https://example.com/de/impressum">',
        '<link rel="alternate" hreflang="en" href="https://example.com/en/impressum">',
        '<link rel="alternate" hreflang="x-default" href="https://example.com/de/impressum">',
      ].join('\n'),
    )
  })

  it('has the same alternates on every variant and its own canonical', () => {
    const de = hreflangLinks({ source: '/impressum', locale: 'de', path: '/de/impressum' }, ORIGIN)
    expect(de.split('\n')[0]).toBe('<link rel="canonical" href="https://example.com/de/impressum">')
    expect(de.split('\n').slice(1)).toEqual(hreflangLinks(variant, ORIGIN).split('\n').slice(1))
  })

  it('points x-default at the German variant, also for the root', () => {
    const out = hreflangLinks({ source: '/', locale: 'en', path: '/en/' }, ORIGIN)
    expect(out).toContain('hreflang="x-default" href="https://example.com/de/"')
  })

  it('percent encodes non ASCII and reserved characters in the path', () => {
    const out = hreflangLinks({ source: '/über&uns', locale: 'de', path: '/de/über&uns' }, ORIGIN)
    expect(out).toContain('href="https://example.com/de/%C3%BCber%26uns"')
    expect(out).not.toContain('ü')
    expect(out).not.toContain('&')
  })

  it('escapes a quote that survives the encoding', () => {
    const out = hreflangLinks({ source: "/it's", locale: 'de', path: "/de/it's" }, ORIGIN)
    expect(out).toContain('href="https://example.com/de/it&#39;s"')
    expect(out).not.toContain("it's")
  })

  it('rejects an invalid origin', () => {
    expect(() => hreflangLinks(variant, 'http://example.com')).toThrow(/origin/i)
  })

  it('rejects a variant that does not match its source and language', () => {
    expect(() => hreflangLinks({ source: '/a', locale: 'en', path: '/de/a' }, ORIGIN)).toThrow(
      /variant/i,
    )
  })
})

describe('sitemapXml', () => {
  const routes = [
    { path: '/', public: true },
    { path: '/impressum', public: true },
    { path: '/app' },
  ]

  function alt(lang: string, href: string): string {
    return `<xhtml:link rel="alternate" hreflang="${lang}" href="${href}"/>`
  }

  function entry(loc: string, hrefs: [string, string, string]): string {
    return [
      '  <url>',
      `    <loc>${loc}</loc>`,
      `    ${alt('de', hrefs[0])}`,
      `    ${alt('en', hrefs[1])}`,
      `    ${alt('x-default', hrefs[2])}`,
      '  </url>',
    ].join('\n')
  }

  it('lists every variant of every public route with its alternates', () => {
    const root: [string, string, string] = [
      'https://example.com/de/',
      'https://example.com/en/',
      'https://example.com/de/',
    ]
    const imp: [string, string, string] = [
      'https://example.com/de/impressum',
      'https://example.com/en/impressum',
      'https://example.com/de/impressum',
    ]
    expect(sitemapXml(routes, ORIGIN)).toBe(
      [
        '<?xml version="1.0" encoding="UTF-8"?>',
        '<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:xhtml="http://www.w3.org/1999/xhtml">',
        entry('https://example.com/de/', root),
        entry('https://example.com/en/', root),
        entry('https://example.com/de/impressum', imp),
        entry('https://example.com/en/impressum', imp),
        '</urlset>',
        '',
      ].join('\n'),
    )
  })

  it('has four url entries with three alternates each for two public routes', () => {
    const xml = sitemapXml(routes, ORIGIN)
    expect(xml.match(/<url>/g)).toHaveLength(4)
    expect(xml.match(/<xhtml:link /g)).toHaveLength(12)
  })

  it('leaves out an app route and carries no lastmod', () => {
    const xml = sitemapXml(routes, ORIGIN)
    expect(xml).not.toContain('/app')
    expect(xml).not.toContain('lastmod')
  })

  it('percent encodes the path and escapes XML characters', () => {
    const xml = sitemapXml([{ path: "/über&it's", public: true }], ORIGIN)
    expect(xml).toContain('<loc>https://example.com/de/%C3%BCber%26it&#39;s</loc>')
    expect(xml).not.toContain('ü')
    expect(xml).not.toMatch(/&(?!#39;)/)
  })

  it('fails without a public route', () => {
    expect(() => sitemapXml([{ path: '/app' }], ORIGIN)).toThrow(/public/i)
    expect(() => sitemapXml([], ORIGIN)).toThrow(/public/i)
  })

  it('accepts exactly 50000 urls and fails above', () => {
    const make = (n: number) =>
      Array.from({ length: n }, (_, i) => ({ path: `/p${i}`, public: true }))
    const perRoute = localeCodes.length
    const fits = Math.floor(50_000 / perRoute)
    expect(() => sitemapXml(make(fits), ORIGIN)).not.toThrow()
    expect(() => sitemapXml(make(fits + 1), ORIGIN)).toThrow(/50000|50,000/)
  })

  it('rejects an invalid origin and an invalid table', () => {
    expect(() => sitemapXml(routes, 'https://example.com/')).toThrow(/origin/i)
    expect(() => sitemapXml([{ path: '/a', public: true }, { path: '/a' }], ORIGIN)).toThrow(
      /duplicate/i,
    )
  })
})
