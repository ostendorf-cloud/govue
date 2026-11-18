// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

/**
 * Legal pages: the imprint, the privacy statement and the terms of a site, on
 * one fixed set of paths in every language.
 *
 * govue carries the structure only: the paths, the route records, the footer
 * links and, in GovueLegalPage, the page shell. The text is the site's own.
 *
 * The canonical page is /<code>/<page>, such as /de/imprint, the same scheme
 * localizeRoutes gives every public route. The Go package language redirects
 * /imprint, /impressum and the other short paths to it.
 *
 * Pure TypeScript with no Vue import, like routes.ts.
 */

import { localeCodes, type LocaleCode } from './catalogues'
import type { RouteDef } from './routes'

/** The legal pages in the order a footer lists them. */
export const LEGAL_PAGES = ['imprint', 'privacy', 'terms'] as const

export type LegalPage = (typeof LEGAL_PAGES)[number]

/**
 * The pages of a site. Imprint and privacy are required, terms is optional:
 * a site without terms leaves it out, and passes WithoutTerms to the Go side.
 */
export interface LegalPages<C = unknown> {
  imprint: C
  privacy: C
  terms?: C
}

/**
 * One page in one language, shaped like a vue-router route record, so a site
 * with vue-router passes the list to createRouter as it is. govue does not
 * depend on vue-router.
 */
export interface LegalRouteRecord<C = unknown> {
  /** The canonical path, such as `/de/imprint`. */
  path: string
  /** Unique per page and language, such as `govue-legal-imprint-de`. */
  name: string
  component: C
  meta: { locale: LocaleCode; legalPage: LegalPage }
}

/** A link of the footer, the same shape GovueFooter takes. */
export interface LegalLink {
  page: LegalPage
  href: string
  label: string
}

/** The pages present in pages, in footer order. */
function presentPages(pages: { terms?: unknown }): LegalPage[] {
  return LEGAL_PAGES.filter((page) => page !== 'terms' || pages.terms !== undefined)
}

/**
 * The canonical path of one legal page in one language. Both arguments come
 * from fixed lists, so the result is one of a fixed set of paths. An unknown
 * page or code throws.
 */
export function legalPath(page: LegalPage, locale: LocaleCode): string {
  if (!LEGAL_PAGES.includes(page)) {
    throw new Error(`govue legal: unknown page ${JSON.stringify(page)}`)
  }
  if (!localeCodes.includes(locale)) {
    throw new Error(`govue legal: unknown language ${JSON.stringify(locale)}`)
  }
  return `/${locale}/${page}`
}

/**
 * The route table entries of the legal pages, for localizeRoutes,
 * localizedFiles and sitemapXml: `{ path: '/imprint', public: true }` and so
 * on. Terms is included only when pages has it.
 */
export function legalRouteDefs(pages: { terms?: unknown }): RouteDef[] {
  return presentPages(pages).map((page) => ({ path: `/${page}`, public: true }))
}

/**
 * One route record per legal page and language, for a site's router or its
 * render callback. imprint and privacy are required.
 */
export function legalRoutes<C>(pages: LegalPages<C>): LegalRouteRecord<C>[] {
  for (const page of ['imprint', 'privacy'] as const) {
    if (pages[page] === undefined || pages[page] === null) {
      throw new Error(`govue legal: the ${page} page is required`)
    }
  }
  const records: LegalRouteRecord<C>[] = []
  for (const page of presentPages(pages)) {
    const component = pages[page] as C
    for (const locale of localeCodes) {
      records.push({
        path: legalPath(page, locale),
        name: `govue-legal-${page}-${locale}`,
        component,
        meta: { locale, legalPage: page },
      })
    }
  }
  return records
}

/**
 * The footer links of the given pages in one language. t reads the label,
 * `govue.legal.link.<page>`, from the language layer.
 */
export function legalLinks(
  locale: LocaleCode,
  pages: readonly LegalPage[],
  t: (key: string) => string,
): LegalLink[] {
  return LEGAL_PAGES.filter((page) => pages.includes(page)).map((page) => ({
    page,
    href: legalPath(page, locale),
    label: t(`govue.legal.link.${page}`),
  }))
}
