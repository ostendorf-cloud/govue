// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'

import { DEFAULT_LOCALE, localeCodes } from './catalogues'
import {
  buildSitemap,
  hreflangLinks,
  localeFromPath,
  localizedPath,
  localizeRoutes,
  SITEMAP_MAX_BYTES,
  SITEMAP_MAX_LOC_LENGTH,
  SITEMAP_MAX_URLS,
  sitemapXml,
  validateOrigin,
  validateRoutes,
} from './routes'

const ORIGIN = 'https://example.com'

/** The prefixed path of a plain ASCII source path, written out by hand. */
function prefixed(source: string, code: string): string {
  return source === '/' ? `/${code}/` : `/${code}${source}`
}

/** Every language, then x-default on the default language: [hreflang, absolute URL]. */
function expectedAlternates(source: string): [string, string][] {
  const list: [string, string][] = localeCodes.map((code) => [
    code,
    `${ORIGIN}${prefixed(source, code)}`,
  ])
  list.push(['x-default', `${ORIGIN}${prefixed(source, DEFAULT_LOCALE)}`])
  return list
}

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
    ['tab', '/a\tb'],
    ['newline', '/a\nb'],
    ['null character', '/a\u0000b'],
    ['DEL', '/a\u007fb'],
    ['no break space', '/a b'],
    ['line separator', '/a b'],
    ['paragraph separator', '/a b'],
    ['zero width space', '/a​b'],
    ['byte order mark', '/a﻿b'],
    ['lone high surrogate', '/a\ud800'],
    ['lone low surrogate', '/a\udc00b'],
    ['percent sign', '/a%20b'],
    ['language prefix', '/de/impressum'],
    ['bare language prefix', '/en'],
  ]
  it.each(bad)('rejects %s', (_name, path) => {
    expect(() => validateRoutes([{ path }])).toThrow(/^govue route:/)
  })

  it('wraps a lone surrogate in a govue error, not a URIError', () => {
    let caught: unknown
    try {
      validateRoutes([{ path: '/a\ud800' }])
    } catch (error) {
      caught = error
    }
    expect(caught).toBeInstanceOf(Error)
    expect(caught).not.toBeInstanceOf(URIError)
  })

  it('names the trailing slash, not an empty segment', () => {
    expect(() => validateRoutes([{ path: '/impressum/' }])).toThrow(/end with/)
  })

  it('says that a path is decoded text when it holds a percent sign', () => {
    expect(() => validateRoutes([{ path: '/a%20b' }])).toThrow(/decoded/)
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
    ['upper case scheme', 'HTTPS://example.com'],
    ['a default port', 'https://example.com:443'],
    ['a host that is not punycode', 'https://exämple.com'],
  ]
  it.each(bad)('rejects %s', (_name, origin) => {
    expect(() => validateOrigin(origin)).toThrow(/^govue origin:/)
  })

  it('says what is wrong with a malformed origin', () => {
    expect(() => validateOrigin('https://Example.com')).toThrow(/punycode/)
    expect(() => validateOrigin('https://example.com:443')).toThrow(/default port/)
  })

  it('rejects port 0 and names it', () => {
    expect(() => validateOrigin('https://example.com:0')).toThrow(/port 0/)
  })

  it('rejects a trailing dot in the host and names it', () => {
    expect(() => validateOrigin('https://example.com.')).toThrow(/trailing dot/)
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

  it('tolerates a single trailing slash on the input', () => {
    expect(localizedPath('/de/impressum/', 'en')).toBe('/en/impressum')
    expect(localizedPath('/impressum/', 'en')).toBe('/en/impressum')
    expect(localizedPath('/a/b/', 'en')).toBe('/en/a/b')
  })

  it('throws for an empty segment, also behind a trailing slash', () => {
    expect(() => localizedPath('//', 'de')).toThrow(/^govue route:/)
    expect(() => localizedPath('/en//', 'de')).toThrow(/^govue route:/)
    expect(() => localizedPath('/impressum//', 'de')).toThrow(/^govue route:/)
    expect(() => localizedPath('/a//b/', 'de')).toThrow(/^govue route:/)
    expect(() => localizedPath('/en//x', 'de')).toThrow(/^govue route:/)
  })

  it('does not strip a first segment that only starts like a language', () => {
    expect(localizedPath('/deutsch', 'en')).toBe('/en/deutsch')
  })

  it('takes decoded text and rejects a percent encoded pathname', () => {
    expect(localizedPath('/de/über', 'en')).toBe('/en/über')
    expect(() => localizedPath('/de/%C3%BCber', 'en')).toThrow(/decoded/)
  })

  it('throws for an unknown language and for an invalid path', () => {
    expect(() => localizedPath('/a', 'fr' as never)).toThrow(/language/i)
    expect(() => localizedPath('//evil.example.com', 'de')).toThrow(/^govue route:/)
    expect(() => localizedPath('/a/../b', 'de')).toThrow(/^govue route:/)
  })
})

describe('localeFromPath', () => {
  it('reads the language of a prefixed path', () => {
    for (const code of localeCodes) {
      expect(localeFromPath(`/${code}/`)).toBe(code)
      expect(localeFromPath(`/${code}`)).toBe(code)
      expect(localeFromPath(`/${code}/impressum`)).toBe(code)
    }
  })

  it('returns undefined without a prefix', () => {
    expect(localeFromPath('/')).toBeUndefined()
    expect(localeFromPath('/impressum')).toBeUndefined()
    expect(localeFromPath('/deutsch')).toBeUndefined()
    expect(localeFromPath('')).toBeUndefined()
  })

  it('needs the leading slash', () => {
    for (const code of localeCodes) {
      expect(localeFromPath(`x/${code}`)).toBeUndefined()
      expect(localeFromPath(`${code}/x`)).toBeUndefined()
    }
  })

  it('matches the exact code only', () => {
    expect(localeFromPath('/EN/')).toBeUndefined()
    expect(localeFromPath('/fr/')).toBeUndefined()
  })
})

describe('localizeRoutes', () => {
  it('returns one variant per language for every public route', () => {
    const sources = ['/', '/impressum']
    const expected = sources.flatMap((source) =>
      localeCodes.map((locale) => ({ source, locale, path: prefixed(source, locale) })),
    )
    expect(localizeRoutes(sources.map((path) => ({ path, public: true })))).toEqual(expected)
  })

  it('leaves out app routes and routes without the flag', () => {
    const out = localizeRoutes([{ path: '/app' }, { path: '/x', public: false }])
    expect(out).toEqual([])
  })

  it('validates the table', () => {
    expect(() => localizeRoutes([{ path: '/a/', public: true }])).toThrow(/^govue route:/)
  })
})

describe('hreflangLinks', () => {
  const variant = { source: '/impressum', locale: 'en', path: '/en/impressum' } as const

  function expectedLines(own: string, source: string): string[] {
    return [
      `<link rel="canonical" href="${ORIGIN}${own}">`,
      ...expectedAlternates(source).map(
        ([lang, href]) => `<link rel="alternate" hreflang="${lang}" href="${href}">`,
      ),
    ]
  }

  it('emits the canonical, every language and x-default', () => {
    expect(hreflangLinks(variant, ORIGIN)).toBe(
      expectedLines('/en/impressum', '/impressum').join('\n'),
    )
  })

  it('has the same alternates on every variant and its own canonical', () => {
    for (const locale of localeCodes) {
      const own = prefixed('/impressum', locale)
      const out = hreflangLinks({ source: '/impressum', locale, path: own }, ORIGIN)
      expect(out).toBe(expectedLines(own, '/impressum').join('\n'))
    }
  })

  it('points x-default at the default language variant, also for the root', () => {
    const out = hreflangLinks({ source: '/', locale: 'en', path: '/en/' }, ORIGIN)
    expect(out).toContain(`hreflang="x-default" href="${ORIGIN}/${DEFAULT_LOCALE}/"`)
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

  it('validates the source like a route table does', () => {
    const bad = ['/de/a', '/a/', '/a?x=1', '/a//b', '/a%20b', 'a', '/a\ud800']
    for (const source of bad) {
      const v = { source, locale: 'en', path: '/en/a' } as const
      expect(() => hreflangLinks(v, ORIGIN)).toThrow(/^govue route:/)
    }
  })
})

describe('sitemapXml', () => {
  const routes = [
    { path: '/', public: true },
    { path: '/impressum', public: true },
    { path: '/app' },
  ]
  const publicSources = ['/', '/impressum']

  function entry(source: string, locale: string): string {
    return [
      '  <url>',
      `    <loc>${ORIGIN}${prefixed(source, locale)}</loc>`,
      ...expectedAlternates(source).map(
        ([lang, href]) => `    <xhtml:link rel="alternate" hreflang="${lang}" href="${href}"/>`,
      ),
      '  </url>',
    ].join('\n')
  }

  it('lists every variant of every public route with its alternates', () => {
    expect(sitemapXml(routes, ORIGIN)).toBe(
      [
        '<?xml version="1.0" encoding="UTF-8"?>',
        '<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:xhtml="http://www.w3.org/1999/xhtml">',
        ...publicSources.flatMap((source) => localeCodes.map((code) => entry(source, code))),
        '</urlset>',
        '',
      ].join('\n'),
    )
  })

  it('has one url entry per variant and one alternate per language plus x-default', () => {
    const xml = sitemapXml(routes, ORIGIN)
    const urls = publicSources.length * localeCodes.length
    expect(xml.match(/<url>/g)).toHaveLength(urls)
    expect(xml.match(/<xhtml:link /g)).toHaveLength(urls * (localeCodes.length + 1))
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
    const fits = Math.floor(SITEMAP_MAX_URLS / perRoute)
    expect(SITEMAP_MAX_URLS).toBe(50_000)
    expect(() => sitemapXml(make(fits), ORIGIN)).not.toThrow()
    expect(() => sitemapXml(make(fits + 1), ORIGIN)).toThrow(/50000|50,000/)
  })

  it('accepts a document of exactly the byte limit and fails one byte above', () => {
    const size = new TextEncoder().encode(sitemapXml(routes, ORIGIN)).length
    const limits = { maxUrls: SITEMAP_MAX_URLS, maxLocLength: SITEMAP_MAX_LOC_LENGTH }
    expect(() => buildSitemap(routes, ORIGIN, { ...limits, maxBytes: size })).not.toThrow()
    expect(() => buildSitemap(routes, ORIGIN, { ...limits, maxBytes: size - 1 })).toThrow(/bytes/)
  })

  it('fails above 50 MiB with the real limit', () => {
    expect(SITEMAP_MAX_BYTES).toBe(50 * 1024 * 1024)
    const pad = 'a'.repeat(1900)
    const big = Array.from({ length: 3500 }, (_, i) => ({ path: `/p${i}${pad}`, public: true }))
    expect(() => sitemapXml(big, ORIGIN)).toThrow(/bytes/)
  })

  it('accepts a url of exactly 2048 characters and fails at 2049', () => {
    expect(SITEMAP_MAX_LOC_LENGTH).toBe(2048)
    const longest = Math.max(...localeCodes.map((code) => code.length))
    // Origin, then "/code", then the source path "/" plus the padding.
    const fill = SITEMAP_MAX_LOC_LENGTH - ORIGIN.length - (1 + longest) - 1
    const fits = [{ path: `/${'a'.repeat(fill)}`, public: true }]
    const tooLong = [{ path: `/${'a'.repeat(fill + 1)}`, public: true }]
    expect(() => sitemapXml(fits, ORIGIN)).not.toThrow()
    expect(() => sitemapXml(tooLong, ORIGIN)).toThrow(/2048/)
  })

  it('measures the url after percent encoding', () => {
    const longest = Math.max(...localeCodes.map((code) => code.length))
    // Each "ü" becomes six characters.
    const fill = SITEMAP_MAX_LOC_LENGTH - ORIGIN.length - (1 + longest) - 1
    const count = Math.floor(fill / 6) + 1
    expect(() => sitemapXml([{ path: `/${'ü'.repeat(count)}`, public: true }], ORIGIN)).toThrow(
      /2048/,
    )
  })

  it('rejects an invalid origin and an invalid table', () => {
    expect(() => sitemapXml(routes, 'https://example.com/')).toThrow(/origin/i)
    expect(() => sitemapXml([{ path: '/a', public: true }, { path: '/a' }], ORIGIN)).toThrow(
      /duplicate/i,
    )
  })
})
