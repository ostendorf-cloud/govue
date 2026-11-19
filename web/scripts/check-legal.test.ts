// @vitest-environment node
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { spawnSync } from 'node:child_process'
import { mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { anchorHrefs, checkLegal, linkTarget, main, parseArgs } from './check-legal.mjs'

const script = join(dirname(fileURLToPath(import.meta.url)), 'check-legal.mjs')

let dir: string

beforeEach(() => {
  dir = mkdtempSync(join(tmpdir(), 'govue-check-legal-'))
})

afterEach(() => {
  rmSync(dir, { recursive: true, force: true })
  vi.restoreAllMocks()
})

/** A footer that links the three legal pages of one language. */
function footer(lang: string, pages = ['imprint', 'privacy', 'terms']): string {
  const links = pages.map((p) => `<a href="/${lang}/${p}">${p}</a>`).join('')
  return `<footer>${links}</footer>`
}

function page(lang: string, body = footer(lang)): string {
  return `<!doctype html>\n<html lang="${lang}" class="x"><head></head><body><h1>t</h1>${body}</body></html>`
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
    writeFileSync(file, page(options.lang?.(path) ?? lang, footer(lang)))
  }
  const locs = (options.sitemap ?? paths)
    .map((p) => `<url><loc>${p.startsWith('/') ? `https://example.com${p}` : p}</loc></url>`)
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
    expect(notes.join('\n')).toMatch(
      /terms page: none, which is right for a site that sells nothing/,
    )
  })

  it('ignores a loc inside a sitemap comment', () => {
    site(BOTH, { sitemap: BOTH.filter((p) => p !== '/en/imprint') })
    const xml = join(dir, 'sitemap.xml')
    writeFileSync(
      xml,
      readFileSync(xml, 'utf8').replace(
        '</urlset>',
        '<!-- <url><loc>https://example.com/en/imprint</loc></url> --></urlset>',
      ),
    )
    expect(checkLegal({ dir }).errors).toEqual([
      'imprint page /en/imprint is not listed in sitemap.xml',
    ])
  })

  it('ignores an html tag inside a comment', () => {
    site(BOTH, { lang: (p) => (p === '/de/privacy' ? 'en' : p.split('/')[1]) })
    const file = join(dir, 'de', 'privacy', 'index.html')
    writeFileSync(file, `<!-- <html lang="de"> -->${readFileSync(file, 'utf8')}`)
    expect(checkLegal({ dir }).errors).toEqual([
      'privacy page de/privacy/index.html has lang "en", want "de"',
    ])
  })

  it('fails a page whose only html tag sits in a comment', () => {
    site(BOTH)
    writeFileSync(
      join(dir, 'de', 'imprint', 'index.html'),
      `<!-- <html lang="de"> --><p>x</p>${footer('de')}`,
    )
    expect(checkLegal({ dir }).errors).toEqual([
      'imprint page de/imprint/index.html has lang "", want "de"',
    ])
  })

  it('fails a sitemap with more than one origin', () => {
    site(BOTH, {
      sitemap: [...BOTH.filter((p) => p !== '/en/privacy'), 'https://evil.example/en/privacy'],
    })
    expect(checkLegal({ dir }).errors).toEqual([
      'sitemap.xml lists more than one origin: https://evil.example, https://example.com',
    ])
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

/** Overwrites one page of the site written by site(). */
function put(file: string, html: string): void {
  const full = join(dir, file)
  mkdirSync(dirname(full), { recursive: true })
  writeFileSync(full, html)
}

const lang = (file: string) => file.split('/')[0]
const withBody = (file: string, body: string) => put(file, page(lang(file), body))

describe('checkLegal links', () => {
  it('passes a site whose every page links the legal pages of its language', () => {
    site(['/de/', '/en/', ...WITH_TERMS])
    expect(checkLegal({ dir }).errors).toEqual([])
  })

  it('fails a page with no footer and names it', () => {
    site(['/de/', '/en/', ...BOTH])
    withBody('de/index.html', '<p>no footer</p>')
    expect(checkLegal({ dir }).errors).toEqual([
      'de/index.html has no link to the imprint page in de: want an a href to /de/imprint',
      'de/index.html has no link to the privacy page in de: want an a href to /de/privacy',
    ])
  })

  it('fails a link to the wrong language', () => {
    site(BOTH)
    withBody(
      'en/privacy/index.html',
      footer('de', ['imprint', 'privacy']) + footer('en', ['imprint']),
    )
    expect(checkLegal({ dir }).errors).toEqual([
      'en/privacy/index.html has no link to the privacy page in en: want an a href to /en/privacy',
    ])
  })

  it('fails a link that sits only in a comment', () => {
    site(BOTH)
    withBody('de/imprint/index.html', `<!-- ${footer('de')} -->${footer('de', ['privacy'])}`)
    expect(checkLegal({ dir }).errors).toEqual([
      'de/imprint/index.html has no link to the imprint page in de: want an a href to /de/imprint',
    ])
  })

  it.each([
    ['script', '<script>var s = \'<a href="/de/imprint">x</a>\'</script>'],
    ['style', '<style>a[href="/de/imprint"]::after{content:"<a href=/de/imprint>"}</style>'],
    ['textarea', '<textarea><a href="/de/imprint">x</a></textarea>'],
    ['link element', '<link rel="help" href="/de/imprint">'],
    ['data attribute', '<div data-href="/de/imprint" title=\'<a href="/de/imprint">\'></div>'],
    ['button href', '<button href="/de/imprint">x</button>'],
    ['template', '<template><a href="/de/imprint">x</a></template>'],
    ['absolute URL', '<a href="https://example.com/de/imprint">x</a>'],
    ['document relative', '<a href="imprint">x</a><a href="../imprint">x</a>'],
  ])('does not count a link in a %s', (_name, html) => {
    site(BOTH)
    withBody('de/privacy/index.html', html + footer('de', ['privacy']))
    expect(checkLegal({ dir }).errors).toEqual([
      'de/privacy/index.html has no link to the imprint page in de: want an a href to /de/imprint',
    ])
  })

  it('needs no terms link on a site without terms', () => {
    site(BOTH)
    withBody('de/imprint/index.html', footer('de', ['imprint', 'privacy']))
    expect(checkLegal({ dir }).errors).toEqual([])
  })

  it('fails a page without the terms link when the site has terms', () => {
    site(['/de/', ...WITH_TERMS])
    withBody('de/index.html', footer('de', ['imprint', 'privacy']))
    expect(checkLegal({ dir }).errors).toEqual([
      'de/index.html has no link to the terms page in de: want an a href to /de/terms',
    ])
  })

  it('needs the terms link on every page when terms are required', () => {
    site(BOTH)
    withBody('en/imprint/index.html', footer('en', ['imprint', 'privacy']))
    expect(checkLegal({ dir, requireTerms: true }).errors).toContain(
      'en/imprint/index.html has no link to the terms page in en: want an a href to /en/terms',
    )
  })

  it('fails a legal page that lacks the cross link', () => {
    site(WITH_TERMS)
    withBody('de/terms/index.html', footer('de', ['privacy', 'terms']))
    withBody('en/privacy/index.html', footer('en', ['imprint', 'terms']))
    expect(checkLegal({ dir }).errors).toEqual([
      'de/terms/index.html has no link to the imprint page in de: want an a href to /de/imprint',
      'en/privacy/index.html has no link to the privacy page in en: want an a href to /en/privacy',
    ])
  })

  it('reports every failure of every page before it returns', () => {
    site(['/de/', '/en/', ...BOTH])
    withBody('de/index.html', '')
    withBody('en/index.html', '')
    withBody('en/privacy/index.html', '')
    expect(checkLegal({ dir }).errors).toHaveLength(6)
  })

  it('checks a page in a nested directory and a file that is not index.html', () => {
    site(BOTH)
    withBody('de/blog/post/index.html', '<p>x</p>')
    withBody('en/404.html', '<p>x</p>')
    expect(checkLegal({ dir }).errors.map((e) => e.split(' ')[0])).toEqual([
      'de/blog/post/index.html',
      'de/blog/post/index.html',
      'en/404.html',
      'en/404.html',
    ])
  })

  it('does not check a page outside the language directories', () => {
    site(BOTH)
    put('index.html', '<p>redirect</p>')
    expect(checkLegal({ dir }).errors).toEqual([])
  })

  it.each([
    ['uppercase', '<A HREF="/de/imprint">x</A>'],
    ['single quoted', "<a href='/de/imprint'>x</a>"],
    ['unquoted', '<a href=/de/imprint>x</a>'],
    ['trailing slash', '<a href="/de/imprint/">x</a>'],
    ['index.html', '<a href="/de/imprint/index.html">x</a>'],
    ['query and fragment', '<a href="/de/imprint?x=1#top">x</a>'],
    ['other attributes', '<a class="x" data-a=\'>\' href = "/de/imprint" rel=noopener>x</a>'],
    ['character reference', '<a href="&#47;de&sol;imprint">x</a>'],
    ['padded', '<a href=" /de/imprint ">x</a>'],
  ])('accepts a %s href', (_name, link) => {
    site(BOTH)
    withBody('de/privacy/index.html', link + footer('de', ['privacy']))
    expect(checkLegal({ dir }).errors).toEqual([])
  })
})

describe('anchorHrefs', () => {
  it.each([
    ['a comment', '<!-- <a href="/x"> --><a href="/y">'],
    ['a gt inside a comment', '<!-- > <a href="/x"> --><a href="/y">'],
    ['an empty comment', '<!--><a href="/y"><!---><!-- a --!><a href="/z">'],
    ['an unclosed comment', '<a href="/y"><!-- <a href="/x">'],
    ['CDATA', '<![CDATA[ <a href="/x"> ]]><a href="/y">'],
    ['a bogus comment', '<?php <a href="/x"> ?><a href="/y">'],
    ['script raw text', '<SCRIPT>"</a><a href=\'/x\'>"</SCRIPT ><a href="/y">'],
    ['style raw text', '<style>/* <a href="/x"> */</style><a href="/y">'],
    ['a stray lt', 'a < b <3 <a href="/y">'],
    ['a duplicated attribute', '<a href="/y" href="/x">'],
    ['a gt in a quoted value', '<a title="a>b" href="/y">'],
    ['no space before the slash', '<a href="/y"/>'],
  ])('reads past %s', (_name, html) => {
    expect(anchorHrefs(html).filter((h) => h !== '/z')).toEqual(['/y'])
  })

  it('drops a tag the input ends in', () => {
    expect(anchorHrefs('<a href="/y')).toEqual([])
    expect(anchorHrefs('<a href=')).toEqual([])
    expect(anchorHrefs('<a')).toEqual([])
  })

  it('ignores everything after plaintext', () => {
    expect(anchorHrefs('<plaintext><a href="/x">')).toEqual([])
  })

  it('does not take an a-prefixed element for an a element', () => {
    expect(anchorHrefs('<abbr href="/x"><area href="/x"><a-b href="/x">')).toEqual([])
  })
})

describe('linkTarget', () => {
  it.each([
    ['/de/imprint', '/de/imprint'],
    ['/de/imprint/', '/de/imprint'],
    ['/de/imprint/index.html', '/de/imprint'],
    ['/de/imprint?a=b#c', '/de/imprint'],
    ['/de/im\tprint', '/de/imprint'],
    ['https://example.com/de/imprint', undefined],
    ['//example.com/de/imprint', undefined],
    ['/\\example.com/de/imprint', undefined],
    ['de/imprint', undefined],
    ['/de/imprint.html', '/de/imprint.html'],
    ['/De/imprint', '/De/imprint'],
  ])('%s', (href, want) => {
    expect(linkTarget(href)).toBe(want)
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

  it('reads --dir=d and --languages=a,b', () => {
    expect(parseArgs(['--dir=out/site', '--languages=de,en'])).toEqual({
      dir: 'out/site',
      languages: ['de', 'en'],
      requireTerms: false,
      help: false,
    })
    expect(parseArgs(['--dir=a=b']).dir).toBe('a=b')
  })

  it('refuses a value on a flag that takes none', () => {
    expect(() => parseArgs(['--require-terms=yes'])).toThrow(/unknown argument/)
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
    expect(ok.stdout).toContain('terms page: none, which is right for a site that sells nothing')

    const eq = spawnSync(process.execPath, [script, `--dir=${dir}`], { encoding: 'utf8' })
    expect(eq.status).toBe(0)
  })
})
