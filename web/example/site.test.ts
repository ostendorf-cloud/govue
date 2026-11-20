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
      'de/imprint/index.html',
      'de/index.html',
      'de/privacy/index.html',
      'de/terms/index.html',
      'en/contact/index.html',
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
