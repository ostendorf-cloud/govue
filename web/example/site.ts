// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

/**
 * The SSR entry of the example site, the shape a site hands to
 * `govue-prerender --out-dir`. A real site imports from
 * `@ostendorf-cloud/govue` and `@ostendorf-cloud/govue/server` instead of the
 * relative paths. After the prerender a site runs
 * `govue-check-legal --dir <out-dir>`.
 */

import { h, type Component } from 'vue'

import { render } from '../src/entry-server'
import { legalRouteDefs, legalRoutes, type LegalPages } from '../src/i18n/legal'
import { createGovueI18n } from '../src/i18n/plugin'
import type { LocalizedRoute, RouteDef } from '../src/i18n/routes'
import { localizedFiles, type PrerenderFile } from '../src/localizedFiles'
import HomePage from './HomePage.vue'
import PlaceholderLegalPage from './PlaceholderLegalPage.vue'

export const ORIGIN = 'https://example.com'

/** One component per legal page. Leave terms out for a site without terms. */
const pages: LegalPages<Component> = {
  imprint: { render: () => h(PlaceholderLegalPage, { page: 'imprint' }) },
  privacy: { render: () => h(PlaceholderLegalPage, { page: 'privacy' }) },
  terms: { render: () => h(PlaceholderLegalPage, { page: 'terms' }) },
}

export const routes: RouteDef[] = [{ path: '/', public: true }, ...legalRouteDefs(pages)]

/** The records a router would get, used here to find the page of a variant. */
const records = legalRoutes(pages)

function componentFor(variant: LocalizedRoute): Component {
  return records.find((record) => record.path === variant.path)?.component ?? HomePage
}

export function files(template: string): Promise<PrerenderFile[]> {
  return localizedFiles(routes, {
    origin: ORIGIN,
    template,
    render: (variant) =>
      render(componentFor(variant), {
        setup: (app) => {
          app.use(createGovueI18n({ locale: variant.locale }))
        },
      }),
  })
}
