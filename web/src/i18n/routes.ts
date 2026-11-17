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

function hasForbiddenCharacter(path: string): boolean {
  for (let i = 0; i < path.length; i++) {
    const code = path.charCodeAt(i)
    // Control characters, space, DEL and the backslash.
    if (code <= 0x20 || code === 0x7f || code === 0x5c) return true
  }
  return false
}

/** Checks the shape of one path and throws a message naming it. */
function checkPath(path: unknown): asserts path is string {
  if (typeof path !== 'string' || !path.startsWith('/')) {
    throw new Error(`govue route: path ${JSON.stringify(path)} must start with "/"`)
  }
  if (hasForbiddenCharacter(path)) {
    throw new Error(`govue route: path ${JSON.stringify(path)} has a space, control or backslash`)
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

/** The language of the first segment, when it is exactly a configured code. */
export function localeFromPath(path: string): LocaleCode | undefined {
  if (!path.startsWith('/')) return undefined
  const first = path.split('/')[1]
  return isLocaleCode(first) ? first : undefined
}

/**
 * validateRoutes throws for the first problem in a route table: a bad path, a
 * path that already starts with a language prefix, a duplicate or a public
 * flag that is not a boolean.
 */
export function validateRoutes(routes: readonly RouteDef[]): void {
  const seen = new Set<string>()
  for (const route of routes) {
    checkPath(route.path)
    if (localeFromPath(route.path) !== undefined) {
      throw new Error(`govue route: path "${route.path}" already starts with a language prefix`)
    }
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
  // url.origin drops a path, a query, a fragment and userinfo and lower cases
  // the host, so an exact match means the input had none of them.
  if (url.origin !== origin) {
    throw new Error(`govue origin: "${origin}" must be scheme, lower case host and optional port`)
  }
  return origin
}

/**
 * localizedPath puts the language prefix in front of a path. A path that
 * already has a prefix gets it swapped, so the language switch can pass
 * `location.pathname`. The root becomes `/en/`.
 */
export function localizedPath(path: string, code: LocaleCode): string {
  if (!isLocaleCode(code)) {
    throw new Error(`govue route: unknown language "${String(code)}"`)
  }
  let rest = path
  const current = localeFromPath(path)
  if (current !== undefined) {
    rest = path.slice(current.length + 1) || '/'
    // `/en/` is the prefix root, not a path with a trailing slash.
    if (rest === '/' || rest === '') return `/${code}/`
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

function escapeMarkup(text: string): string {
  return text
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&#39;')
}

/** The absolute, percent encoded, markup safe URL of a prefixed path. */
function absoluteUrl(origin: string, path: string): string {
  const encoded = path.split('/').map(encodeURIComponent).join('/')
  return escapeMarkup(origin + encoded)
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
  if (localizedPath(variant.source, variant.locale) !== variant.path) {
    throw new Error(`govue route: variant "${variant.path}" does not match its source and language`)
  }
  const lines = [`<link rel="canonical" href="${absoluteUrl(origin, variant.path)}">`]
  for (const alt of alternates(variant.source, origin)) {
    lines.push(`<link rel="alternate" hreflang="${alt.hreflang}" href="${alt.href}">`)
  }
  return lines.join('\n')
}

/**
 * sitemapXml returns the sitemap of the public routes: one url per language
 * variant, each with the alternates of all languages and x-default. There is
 * no lastmod. It throws without a public route and above 50,000 urls.
 */
export function sitemapXml(routes: readonly RouteDef[], origin: string): string {
  validateOrigin(origin)
  const variants = localizeRoutes(routes)
  if (variants.length === 0) {
    throw new Error('govue sitemap: the route table has no public route')
  }
  if (variants.length > SITEMAP_MAX_URLS) {
    throw new Error(
      `govue sitemap: ${variants.length} urls exceed the limit of ${SITEMAP_MAX_URLS} per file`,
    )
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
  return lines.join('\n')
}
