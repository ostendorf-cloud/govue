// @vitest-environment node
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { spawnSync } from 'node:child_process'
import { mkdirSync, mkdtempSync, rmSync, symlinkSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { checkLegal, main, parseArgs } from './check-legal.mjs'

const script = join(dirname(fileURLToPath(import.meta.url)), 'check-legal.mjs')

let dir: string

beforeEach(() => {
  dir = mkdtempSync(join(tmpdir(), 'govue-check-legal-'))
})

afterEach(() => {
  rmSync(dir, { recursive: true, force: true })
  vi.restoreAllMocks()
})

function page(lang: string): string {
  return `<!doctype html>\n<html lang="${lang}" class="x"><head></head><body><h1>t</h1></body></html>`
}

/** Writes a prerender output with the given pages, each as /<lang>/<page>. */
function site(
  paths: string[],
  options: { sitemap?: string[]; lang?: (path: string) => string } = {},
): void {
  for (const path of paths) {
    const [, lang, name] = path.split('/')
    const file = join(dir, lang, name, 'index.html')
    mkdirSync(dirname(file), { recursive: true })
    writeFileSync(file, page(options.lang?.(path) ?? lang))
  }
  const locs = (options.sitemap ?? paths)
    .map((p) => `<url><loc>https://example.com${p}</loc></url>`)
    .join('')
  writeFileSync(
    join(dir, 'sitemap.xml'),
    `<?xml version="1.0" encoding="UTF-8"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">${locs}</urlset>`,
  )
}

const BOTH = ['/de/imprint', '/en/imprint', '/de/privacy', '/en/privacy']
const WITH_TERMS = [...BOTH, '/de/terms', '/en/terms']

describe('checkLegal', () => {
  it('passes imprint and privacy in both languages, terms optional', () => {
    site(['/de/', '/en/', ...BOTH])
    const { errors, notes } = checkLegal({ dir })
    expect(errors).toEqual([])
    expect(notes.join('\n')).toMatch(/terms page: none, which is allowed/)
  })

  it('passes with terms in both languages', () => {
    site(WITH_TERMS)
    const { errors, notes } = checkLegal({ dir })
    expect(errors).toEqual([])
    expect(notes).toContain('terms page: present in every language')
  })

  it.each([
    ['/de/imprint', 'imprint page missing in de'],
    ['/en/imprint', 'imprint page missing in en'],
    ['/de/privacy', 'privacy page missing in de'],
    ['/en/privacy', 'privacy page missing in en'],
  ])('fails when %s is missing', (missing, message) => {
    site(BOTH.filter((p) => p !== missing))
    const { errors } = checkLegal({ dir })
    expect(errors.join('\n')).toContain(message)
  })

  it('fails when imprint and privacy are both missing', () => {
    site(['/de/', '/en/'])
    expect(checkLegal({ dir }).errors).toHaveLength(4)
  })

  it('fails a terms page that exists in one language only', () => {
    site([...BOTH, '/de/terms'])
    expect(checkLegal({ dir }).errors).toEqual(['terms page missing in en: no en/terms/index.html'])
  })

  it('fails a terms page listed in the sitemap but not written', () => {
    site(BOTH, { sitemap: [...BOTH, '/en/terms'] })
    expect(checkLegal({ dir }).errors.join('\n')).toMatch(/terms page missing in de/)
  })

  it('fails a missing terms page when terms are required', () => {
    site(BOTH)
    const { errors } = checkLegal({ dir, requireTerms: true })
    expect(errors).toEqual([
      'terms page missing in de: no de/terms/index.html',
      'terms page missing in en: no en/terms/index.html',
    ])
  })

  it('fails a page whose html element carries another language', () => {
    site(BOTH, { lang: (p) => (p === '/en/privacy' ? 'de' : p.split('/')[1]) })
    expect(checkLegal({ dir }).errors).toEqual([
      'privacy page en/privacy/index.html has lang "de", want "en"',
    ])
  })

  it('fails a page without a lang', () => {
    site(BOTH, { lang: (p) => (p === '/de/imprint' ? '' : p.split('/')[1]) })
    expect(checkLegal({ dir }).errors.join('\n')).toMatch(/de\/imprint\/index.html has lang ""/)
  })

  it('fails a page the sitemap does not list, since the server would not serve it', () => {
    site(BOTH, { sitemap: BOTH.filter((p) => p !== '/de/privacy') })
    expect(checkLegal({ dir }).errors).toEqual([
      'privacy page /de/privacy is not listed in sitemap.xml',
    ])
  })

  it('fails without a sitemap', () => {
    site(BOTH)
    rmSync(join(dir, 'sitemap.xml'))
    expect(checkLegal({ dir }).errors.join('\n')).toMatch(/sitemap.xml missing/)
  })

  it('counts a symbolic link as missing', () => {
    site(BOTH.filter((p) => p !== '/en/imprint'))
    mkdirSync(join(dir, 'en', 'imprint'), { recursive: true })
    symlinkSync(join(dir, 'de', 'imprint', 'index.html'), join(dir, 'en', 'imprint', 'index.html'))
    expect(checkLegal({ dir }).errors.join('\n')).toMatch(/imprint page missing in en/)
  })

  it('checks the languages it is given', () => {
    site(BOTH)
    expect(checkLegal({ dir, languages: ['de'] }).errors).toEqual([])
    expect(checkLegal({ dir, languages: ['de', 'en', 'fr'] }).errors).toHaveLength(2)
  })

  it('refuses a malformed language list', () => {
    expect(() => checkLegal({ dir, languages: [] })).toThrow(/each code once/)
    expect(() => checkLegal({ dir, languages: ['de', 'de'] })).toThrow(/each code once/)
    expect(() => checkLegal({ dir, languages: ['../x'] })).toThrow(/lowercase letters/)
  })
})

describe('parseArgs', () => {
  it('reads every flag', () => {
    expect(parseArgs(['--dir', 'dist', '--languages', 'de, en', '--require-terms'])).toEqual({
      dir: 'dist',
      languages: ['de', 'en'],
      requireTerms: true,
      help: false,
    })
  })

  it('rejects an unknown flag', () => {
    expect(() => parseArgs(['--nope'])).toThrow(/unknown argument/)
  })
})

describe('main', () => {
  it('exits 0 for a complete site and 1 for a missing page', () => {
    vi.spyOn(process.stdout, 'write').mockReturnValue(true)
    vi.spyOn(process.stderr, 'write').mockReturnValue(true)
    site(BOTH)
    expect(main(['--dir', dir])).toBe(0)
    rmSync(join(dir, 'en', 'privacy'), { recursive: true })
    expect(main(['--dir', dir])).toBe(1)
  })

  it('exits 1 without --dir and for a bad flag', () => {
    vi.spyOn(process.stderr, 'write').mockReturnValue(true)
    expect(main([])).toBe(1)
    expect(main(['--bad'])).toBe(1)
  })

  it('runs as a CLI and names the problem', () => {
    site(BOTH.filter((p) => p !== '/de/imprint'))
    const failed = spawnSync(process.execPath, [script, '--dir', dir], { encoding: 'utf8' })
    expect(failed.status).toBe(1)
    expect(failed.stderr).toContain('imprint page missing in de')

    site(BOTH)
    const ok = spawnSync(process.execPath, [script, '--dir', dir], { encoding: 'utf8' })
    expect(ok.status).toBe(0)
    expect(ok.stdout).toContain('terms page: none, which is allowed')
  })
})
