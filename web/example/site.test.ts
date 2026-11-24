// @vitest-environment node
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'

import { afterAll, beforeAll, describe, expect, it } from 'vitest'

import { checkLegal } from '../scripts/check-legal.mjs'
import type { PrerenderFile } from '../src/localizedFiles'
import { files } from './site'

const TEMPLATE = `<!doctype html>
<html lang="xx">
<head><meta charset="utf-8"><!--app-head--></head>
<body><div id="app"><!--app-html--></div></body>
</html>
`

let built: PrerenderFile[]
let dir: string

beforeAll(async () => {
  built = await files(TEMPLATE)
  dir = mkdtempSync(join(tmpdir(), 'govue-example-site-'))
  for (const file of built) {
    const full = join(dir, file.path)
    mkdirSync(dirname(full), { recursive: true })
    writeFileSync(full, file.content)
  }
})

afterAll(() => {
  rmSync(dir, { recursive: true, force: true })
})

function page(path: string): string {
  const file = built.find((f) => f.path === path)
  if (!file) throw new Error(`no file ${path}`)
  return file.content
}

describe('example site', () => {
  it('prerenders the start page and the three legal pages in both languages', () => {
    expect(built.map((f) => f.path).sort()).toEqual([
      'de/contact/index.html',
      'de/hero/index.html',
      'de/imprint/index.html',
      'de/index.html',
      'de/privacy/index.html',
      'de/terms/index.html',
      'en/contact/index.html',
      'en/hero/index.html',
      'en/imprint/index.html',
      'en/index.html',
      'en/privacy/index.html',
      'en/terms/index.html',
      'sitemap.xml',
    ])
  })

  it('passes the build check, terms included', () => {
    expect(checkLegal({ dir, requireTerms: true }).errors).toEqual([])
  })

  it('marks every legal page as a placeholder in its language', () => {
    expect(page('de/imprint/index.html')).toContain(
      'Dies ist ein Platzhalter und kein rechtlicher Text.',
    )
    expect(page('en/privacy/index.html')).toContain('This is a placeholder and no legal text.')
    expect(page('de/imprint/index.html')).toContain(
      '<h1 class="text-2xl font-semibold text-text">Impressum</h1>',
    )
    expect(page('en/terms/index.html')).toContain('Terms and conditions</h1>')
  })

  it('prerenders the contact form without a worker, in both languages', () => {
    expect(page('en/contact/index.html')).toContain('Send message')
    expect(page('en/contact/index.html')).toContain('href="/en/privacy"')
    expect(page('de/contact/index.html')).toContain('Nachricht senden')
    expect(page('de/contact/index.html')).toContain('href="/de/privacy"')
    expect(page('en/contact/index.html')).toContain('name="website"')
  })

  it('prerenders the compact theme switch with icons, in both languages', () => {
    const en = page('en/index.html')
    expect(en).toContain('aria-label="Theme"')
    expect(en).toContain('aria-label="Follow the system"')
    expect(en).toContain('aria-label="Light mode"')
    expect(en).toContain('aria-label="Dark mode"')
    expect(en).toContain('aria-hidden="true"')
    const de = page('de/index.html')
    expect(de).toContain('aria-label="Darstellung"')
    expect(de).toContain('aria-label="Dem System folgen"')
    expect(de).toContain('aria-label="Heller Modus"')
    expect(de).toContain('aria-label="Dunkler Modus"')
  })

  it('prerenders the narrow parts below 1024px and the wide ones from there', () => {
    const en = page('en/index.html')
    expect(en).toContain('aria-label="Theme: Follow the system. Next: Light mode"')
    expect(en).toMatch(
      /<span class="contents lg:hidden"><button type="button" aria-label="Theme: Follow/,
    )
    expect(en).toMatch(/<span class="hidden lg:contents"><div role="radiogroup"/)
    // The narrow language part links German only, the wide one both.
    const narrow = en.match(/<span class="contents lg:hidden">\s*<div role="group".*?<\/div>/s)
    expect(narrow?.[0]).toContain('hreflang="de"')
    expect(narrow?.[0]).not.toContain('hreflang="en"')
    const de = page('de/index.html')
    expect(de).toContain('aria-label="Darstellung: Dem System folgen. Als Nächstes: Heller Modus"')
  })

  it('shows the solid sticky header on the start page, with the nav at the end', () => {
    const html = page('en/index.html')

    expect(html).toContain('data-state="solid"')
    expect(html).toContain('sticky')
    expect(html).not.toContain('data-overlay')
    expect(html).toContain('min-[480px]:justify-end')
  })

  it('shows the overlay header over the hero, transparent at the top', () => {
    const html = page('en/hero/index.html')

    expect(html).toContain('data-state="transparent"')
    expect(html).toContain('data-overlay="true"')
    expect(html).toContain('fixed')
    // The hero is dark in both page modes, which the transparent header needs.
    expect(html).toContain('[color-scheme:dark]')
  })

  it('marks the contact link as the call to action and the current page', () => {
    const html = page('en/contact/index.html')

    expect(html).toMatch(/<a href="\/en\/contact" aria-current="page" data-variant="cta"/)
  })

  it('offers every language as a real link to the same page, with hreflang', () => {
    const html = page('en/hero/index.html')

    expect(html).toContain('href="/de/hero"')
    expect(html).toContain('hreflang="de"')
    expect(html).toContain('href="/en/hero"')
    expect(page('de/contact/index.html')).toContain('href="/en/contact"')
  })

  it('links the legal pages from every page in its language', () => {
    for (const lang of ['de', 'en']) {
      for (const path of [`${lang}/index.html`, `${lang}/privacy/index.html`]) {
        const html = page(path)
        for (const legal of ['imprint', 'privacy', 'terms']) {
          expect(html, path).toContain(`href="/${lang}/${legal}"`)
        }
        const other = lang === 'de' ? 'en' : 'de'
        expect(html, path).not.toContain(`href="/${other}/imprint"`)
      }
    }
    expect(page('de/index.html')).toContain('>Impressum</a>')
    expect(page('en/index.html')).toContain('>Imprint</a>')
  })
})
