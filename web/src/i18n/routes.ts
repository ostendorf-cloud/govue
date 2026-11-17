// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

/**
 * Language routes: the route table of a site, the /de/ and /en/ variants of
 * its public pages, and the markup that tells a search engine how they relate.
 *
 * Pure TypeScript with no Vue import, so the prerender build, the sitemap
 * and the client navigation share one implementation. The languages come from
 * catalogues.ts and nowhere else.
 *
 * A route is public only when it says so. A forgotten flag hides a page from
 * the sitemap instead of exposing an app route. App routes keep no prefix and
 * never appear in the output of this module.
 */

import { DEFAULT_LOCALE, isLocaleCode, localeCodes, type LocaleCode } from './catalogues'

export interface RouteDef {
  /** Absolute path such as `/impressum`. No query, fragment or trailing slash. */
  path: string
  /** Only a public route gets language variants. Defaults to false. */
  public?: boolean
}

/** One language variant of a public route. */
export interface LocalizedRoute {
  /** The path as written in the route table. */
  source: string
  locale: LocaleCode
  /** The prefixed path, for example `/en/impressum`. The root is `/en/`. */
  path: string
}

/** The sitemap protocol allows 50,000 URLs per file. */
export const SITEMAP_MAX_URLS = 50_000

/** The sitemap protocol allows 50 MiB uncompressed per file. */
export const SITEMAP_MAX_BYTES = 50 * 1024 * 1024

/** The sitemap protocol allows a URL of at most 2048 characters. */
export const SITEMAP_MAX_LOC_LENGTH = 2048

/**
 * A path is decoded text. These never belong in it: Unicode white space
 * (\p{White_Space}: space, no break space, U+2028, U+2029), control characters
 * (\p{Cc}, which include DEL), format characters (\p{Cf}: zero width space,
 * joiners, byte order mark, bidi marks), a lone surrogate (\p{Cs}) and the
 * backslash.
 */
const FORBIDDEN_CHARACTER = /[\p{White_Space}\p{Cc}\p{Cf}\p{Cs}\\]/u

/** Checks the shape of one path and throws a message naming it. */
function checkPath(path: unknown): asserts path is string {
  if (typeof path !== 'string' || !path.startsWith('/')) {
    throw new Error(`govue route: path ${JSON.stringify(path)} must start with "/"`)
  }
  if (FORBIDDEN_CHARACTER.test(path)) {
    throw new Error(
      `govue route: path ${JSON.stringify(path)} has white space, a control or format character, a lone surrogate or a backslash`,
    )
  }
  if (path.includes('%')) {
    throw new Error(
      `govue route: path ${JSON.stringify(path)} must be decoded text and must not contain "%"`,
    )
  }
  if (path.includes('?') || path.includes('#')) {
    throw new Error(`govue route: path "${path}" must not carry a query or a fragment`)
  }
  if (path === '/') return
  const segments = path.split('/').slice(1)
  if (segments.at(-1) === '') {
    throw new Error(`govue route: path "${path}" must not end with "/"`)
  }
  for (const segment of segments) {
    if (segment === '') throw new Error(`govue route: path "${path}" has an empty segment`)
    if (segment === '.' || segment === '..') {
      throw new Error(`govue route: path "${path}" has a dot segment`)
    }
  }
}

/**
 * The language of the first segment, when it is exactly a configured code.
 * The path is decoded text, not the percent encoded `location.pathname`.
 */
export function localeFromPath(path: string): LocaleCode | undefined {
  if (!path.startsWith('/')) return undefined
  const first = path.split('/')[1]
  return isLocaleCode(first) ? first : undefined
}

/** A route path as written in the table: well formed and without a language prefix. */
function checkRoutePath(path: unknown): asserts path is string {
  checkPath(path)
  // Go registers a page as a ServeMux pattern, where braces are wildcard
  // syntax. A route with one would fail the server's startup instead.
  if (path.includes('{') || path.includes('}')) {
    throw new Error(
      `govue route: path "${path}" must not contain "{" or "}", a Go ServeMux reads them as pattern syntax`,
    )
  }
  if (localeFromPath(path) !== undefined) {
    throw new Error(`govue route: path "${path}" already starts with a language prefix`)
  }
}

/**
 * validateRoutes throws for the first problem in a route table: a bad path, a
 * path that already starts with a language prefix, a duplicate or a public
 * flag that is not a boolean.
 */
export function validateRoutes(routes: readonly RouteDef[]): void {
  const seen = new Set<string>()
  for (const route of routes) {
    checkRoutePath(route.path)
    if (route.public !== undefined && typeof route.public !== 'boolean') {
      throw new Error(`govue route: public of "${route.path}" must be a boolean`)
    }
    if (seen.has(route.path)) {
      throw new Error(`govue route: duplicate path "${route.path}"`)
    }
    seen.add(route.path)
  }
}

/**
 * validateOrigin returns the origin when it is `https://host[:port]` with no
 * path, query, fragment or userinfo. `http` is allowed for localhost only.
 * Internal: every generator validates its origin itself.
 */
export function validateOrigin(origin: string): string {
  let url: URL
  try {
    url = new URL(origin)
  } catch {
    throw new Error(`govue origin: "${origin}" is not a URL`)
  }
  const httpLocal = url.protocol === 'http:' && url.hostname === 'localhost'
  if (url.protocol !== 'https:' && !httpLocal) {
    throw new Error(`govue origin: "${origin}" must use https, or http for localhost`)
  }
  // url.origin drops a path, a query, a fragment and userinfo, lower cases
  // scheme and host, converts a host to punycode and drops a default port, so
  // an exact match means the input had none of that.
  if (url.origin !== origin) {
    throw new Error(
      `govue origin: "${origin}" must be scheme://host[:port] with a lower case scheme and host, a punycode host, no default port and no userinfo, path, query or fragment`,
    )
  }
  if (url.port === '0') {
    throw new Error(`govue origin: "${origin}" must not use port 0`)
  }
  if (url.hostname.endsWith('.')) {
    throw new Error(`govue origin: "${origin}" must not have a trailing dot in the host`)
  }
  return origin
}

/**
 * localizedPath puts the language prefix in front of a path. A path that
 * already has a prefix gets it swapped, so the language switch can pass the
 * decoded `location.pathname`. A single trailing slash on the input is
 * dropped, as the served `/de/impressum/` is the canonical `/de/impressum`.
 * The root becomes `/en/`. The path is decoded text: a `%` throws.
 */
export function localizedPath(path: string, code: LocaleCode): string {
  if (!isLocaleCode(code)) {
    throw new Error(`govue route: unknown language "${String(code)}"`)
  }
  // `//` stays as it is and fails the path check below.
  let text = path
  if (text.length > 1 && text.endsWith('/') && !text.endsWith('//')) text = text.slice(0, -1)
  let rest = text
  const current = localeFromPath(text)
  if (current !== undefined) {
    rest = text.slice(current.length + 1)
    // `/en` is the prefix root.
    if (rest === '') return `/${code}/`
  }
  checkPath(rest)
  return rest === '/' ? `/${code}/` : `/${code}${rest}`
}

/**
 * localizeRoutes returns one variant per language for every public route, in
 * table order and then language order. App routes are not in the result.
 */
export function localizeRoutes(routes: readonly RouteDef[]): LocalizedRoute[] {
  validateRoutes(routes)
  const out: LocalizedRoute[] = []
  for (const route of routes) {
    if (route.public !== true) continue
    for (const locale of localeCodes) {
      out.push({ source: route.path, locale, path: localizedPath(route.path, locale) })
    }
  }
  return out
}

// encodeURIComponent leaves only the apostrophe of these five characters
// unescaped. The other four are defence in depth should the encoding change.
function escapeMarkup(text: string): string {
  return text
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;')
}

/** The absolute, percent encoded URL of a prefixed path, not yet markup safe. */
function plainUrl(origin: string, path: string): string {
  return origin + path.split('/').map(encodeURIComponent).join('/')
}

/** The absolute, percent encoded, markup safe URL of a prefixed path. */
function absoluteUrl(origin: string, path: string): string {
  return escapeMarkup(plainUrl(origin, path))
}

/** The alternates of one source path: every language, then x-default. */
function alternates(source: string, origin: string): { hreflang: string; href: string }[] {
  const list: { hreflang: string; href: string }[] = localeCodes.map((code) => ({
    hreflang: code,
    href: absoluteUrl(origin, localizedPath(source, code)),
  }))
  list.push({
    hreflang: 'x-default',
    href: absoluteUrl(origin, localizedPath(source, DEFAULT_LOCALE)),
  })
  return list
}

/**
 * hreflangLinks returns the head markup of one variant: its own canonical
 * link, one alternate per language and an x-default that points at the
 * default language variant. One tag per line, no indentation.
 */
export function hreflangLinks(variant: LocalizedRoute, origin: string): string {
  validateOrigin(origin)
  checkRoutePath(variant.source)
  if (localizedPath(variant.source, variant.locale) !== variant.path) {
    throw new Error(`govue route: variant "${variant.path}" does not match its source and language`)
  }
  const lines = [`<link rel="canonical" href="${absoluteUrl(origin, variant.path)}">`]
  for (const alt of alternates(variant.source, origin)) {
    lines.push(`<link rel="alternate" hreflang="${alt.hreflang}" href="${alt.href}">`)
  }
  return lines.join('\n')
}

export interface SitemapLimits {
  maxUrls: number
  maxBytes: number
  maxLocLength: number
}

const SITEMAP_LIMITS: SitemapLimits = {
  maxUrls: SITEMAP_MAX_URLS,
  maxBytes: SITEMAP_MAX_BYTES,
  maxLocLength: SITEMAP_MAX_LOC_LENGTH,
}

/**
 * sitemapXml returns the sitemap of the public routes: one url per language
 * variant, each with the alternates of all languages and x-default. There is
 * no lastmod. It throws without a public route, above 50,000 urls, above 50
 * MiB of UTF-8 and for a url longer than 2048 characters.
 */
export function sitemapXml(routes: readonly RouteDef[], origin: string): string {
  return buildSitemap(routes, origin, SITEMAP_LIMITS)
}

/** sitemapXml with the limits as a parameter, so a test can reach a boundary. */
export function buildSitemap(
  routes: readonly RouteDef[],
  origin: string,
  limits: SitemapLimits,
): string {
  validateOrigin(origin)
  const variants = localizeRoutes(routes)
  if (variants.length === 0) {
    throw new Error('govue sitemap: the route table has no public route')
  }
  if (variants.length > limits.maxUrls) {
    throw new Error(
      `govue sitemap: ${variants.length} urls exceed the limit of ${limits.maxUrls} per file`,
    )
  }
  for (const variant of variants) {
    const length = plainUrl(origin, variant.path).length
    if (length > limits.maxLocLength) {
      throw new Error(
        `govue sitemap: the url of "${variant.path}" has ${length} characters, the limit is ${limits.maxLocLength}`,
      )
    }
  }
  const lines = [
    '<?xml version="1.0" encoding="UTF-8"?>',
    '<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:xhtml="http://www.w3.org/1999/xhtml">',
  ]
  for (const variant of variants) {
    lines.push('  <url>', `    <loc>${absoluteUrl(origin, variant.path)}</loc>`)
    for (const alt of alternates(variant.source, origin)) {
      lines.push(`    <xhtml:link rel="alternate" hreflang="${alt.hreflang}" href="${alt.href}"/>`)
    }
    lines.push('  </url>')
  }
  lines.push('</urlset>', '')
  const xml = lines.join('\n')
  const bytes = new TextEncoder().encode(xml).length
  if (bytes > limits.maxBytes) {
    throw new Error(
      `govue sitemap: ${bytes} bytes exceed the limit of ${limits.maxBytes} bytes per file`,
    )
  }
  return xml
}
