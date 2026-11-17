// @vitest-environment node
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'

import GovueSkipLink from './components/GovueSkipLink.vue'
import { render } from './entry-server'
import { hreflangLinks, localizeRoutes, sitemapXml, type LocalizedRoute } from './i18n/routes'
import { createGovueI18n } from './i18n/plugin'
import { HEAD_PLACEHOLDER, localizedFiles, setHtmlLang } from './localizedFiles'

const ORIGIN = 'https://example.com'

const TEMPLATE = `<!doctype html>
<html lang="xx" class="page">
<head><meta charset="utf-8"><!--app-head--></head>
<body><div id="app"><!--app-html--></div></body>
</html>
`

const ROUTES = [
  { path: '/', public: true },
  { path: '/impressum', public: true },
  { path: '/app' },
  { path: '/admin', public: false },
]

/** A render callback that names the variant it was called for. */
function stubRender(variant: LocalizedRoute): Promise<string> {
  return Promise.resolve(`<p>${variant.locale} ${variant.source}</p>`)
}

function page(files: { path: string; content: string }[], path: string): string {
  const file = files.find((f) => f.path === path)
  if (!file) throw new Error(`no file ${path}`)
  return file.content
}

describe('localizedFiles', () => {
  it('writes one page per public route and language, then the sitemap', async () => {
    const files = await localizedFiles(ROUTES, {
      origin: ORIGIN,
      template: TEMPLATE,
      render: stubRender,
    })

    expect(files.map((f) => f.path)).toEqual([
      'de/index.html',
      'en/index.html',
      'de/impressum/index.html',
      'en/impressum/index.html',
      'sitemap.xml',
    ])
  })

  it('leaves app routes out of the pages and the sitemap', async () => {
    const render = vi.fn(stubRender)
    const files = await localizedFiles(ROUTES, { origin: ORIGIN, template: TEMPLATE, render })

    const sources = render.mock.calls.map(([variant]) => variant.source)
    expect(sources).not.toContain('/app')
    expect(sources).not.toContain('/admin')
    expect(files.some((f) => f.path.includes('app') || f.path.includes('admin'))).toBe(false)
    expect(page(files, 'sitemap.xml')).not.toMatch(/app|admin/)
  })

  it('calls render once per variant, in order and one at a time', async () => {
    let running = 0
    const seen: string[] = []
    await localizedFiles(ROUTES, {
      origin: ORIGIN,
      template: TEMPLATE,
      render: async (variant) => {
        running++
        expect(running).toBe(1)
        await new Promise((resolve) => setTimeout(resolve, 1))
        seen.push(variant.path)
        running--
        return ''
      },
    })

    expect(seen).toEqual(localizeRoutes(ROUTES).map((v) => v.path))
  })

  it('puts the markup of each variant at the app placeholder', async () => {
    const files = await localizedFiles(ROUTES, {
      origin: ORIGIN,
      template: TEMPLATE,
      render: stubRender,
    })

    expect(page(files, 'en/impressum/index.html')).toContain(
      '<div id="app"><p>en /impressum</p></div>',
    )
    expect(page(files, 'de/index.html')).toContain('<div id="app"><p>de /</p></div>')
  })

  it('sets the lang of each page and keeps the other attributes', async () => {
    const files = await localizedFiles(ROUTES, {
      origin: ORIGIN,
      template: TEMPLATE,
      render: stubRender,
    })

    expect(page(files, 'de/index.html')).toContain('<html lang="de" class="page">')
    expect(page(files, 'en/impressum/index.html')).toContain('<html lang="en" class="page">')
  })

  it('puts the canonical link and the hreflang set at the head placeholder', async () => {
    const files = await localizedFiles(ROUTES, {
      origin: ORIGIN,
      template: TEMPLATE,
      render: stubRender,
    })
    const html = page(files, 'en/impressum/index.html')

    const variant = { source: '/impressum', locale: 'en', path: '/en/impressum' } as const
    expect(html).toContain(`<meta charset="utf-8">${hreflangLinks(variant, ORIGIN)}</head>`)
    expect(html).toContain('<link rel="canonical" href="https://example.com/en/impressum">')
    const alternates = [...html.matchAll(/hreflang="([^"]+)" href="([^"]+)"/g)].map((m) => [
      m[1],
      m[2],
    ])
    expect(alternates).toEqual([
      ['de', 'https://example.com/de/impressum'],
      ['en', 'https://example.com/en/impressum'],
      ['x-default', 'https://example.com/de/impressum'],
    ])
    expect(html).not.toContain(HEAD_PLACEHOLDER)
    expect(html).not.toContain('<!--app-html-->')
  })

  it('writes the sitemap of the same route table', async () => {
    const files = await localizedFiles(ROUTES, {
      origin: ORIGIN,
      template: TEMPLATE,
      render: stubRender,
    })
    const xml = page(files, 'sitemap.xml')

    expect(xml).toBe(sitemapXml(ROUTES, ORIGIN))
    expect(xml.match(/<url>/g)).toHaveLength(4)
    expect(xml.match(/<xhtml:link /g)).toHaveLength(12)
  })

  it('writes the decoded path of a route with non ASCII text', async () => {
    const files = await localizedFiles([{ path: '/über', public: true }], {
      origin: ORIGIN,
      template: TEMPLATE,
      render: stubRender,
    })

    expect(files.map((f) => f.path)).toContain('de/über/index.html')
    expect(page(files, 'de/über/index.html')).toContain('https://example.com/de/%C3%BCber')
  })

  it('leaves placeholders and tags inside the rendered markup alone', async () => {
    const files = await localizedFiles([{ path: '/', public: true }], {
      origin: ORIGIN,
      template: TEMPLATE,
      render: () => Promise.resolve('<!--app-head--><html lang="zz">'),
    })
    const html = page(files, 'de/index.html')

    expect(html).toContain('<div id="app"><!--app-head--><html lang="zz"></div>')
    expect(html).toContain('<html lang="de" class="page">')
    expect(html).toContain('<meta charset="utf-8"><link rel="canonical"')
  })

  it('renders two languages in one process, each with its own text', async () => {
    const Page = defineComponent({ setup: () => () => h(GovueSkipLink) })
    const files = await localizedFiles([{ path: '/', public: true }], {
      origin: ORIGIN,
      template: TEMPLATE,
      render: (variant) =>
        render(Page, {
          setup: (app) => {
            app.use(createGovueI18n({ locale: variant.locale }))
          },
        }),
    })

    expect(page(files, 'de/index.html')).toContain('Zum Inhalt springen')
    expect(page(files, 'de/index.html')).not.toContain('Skip to content')
    expect(page(files, 'en/index.html')).toContain('Skip to content')
    expect(page(files, 'en/index.html')).not.toContain('Zum Inhalt springen')
  })

  describe('fails before rendering anything', () => {
    const cases: [string, string, RegExp][] = [
      ['no head placeholder', TEMPLATE.replace('<!--app-head-->', ''), /<!--app-head-->/],
      [
        'the head placeholder twice',
        TEMPLATE.replace('<!--app-head-->', '<!--app-head--><!--app-head-->'),
        /<!--app-head-->.*more than once/,
      ],
      ['no app placeholder', TEMPLATE.replace('<!--app-html-->', ''), /<!--app-html-->/],
      [
        'the app placeholder twice',
        TEMPLATE.replace('<!--app-html-->', '<!--app-html--><!--app-html-->'),
        /<!--app-html-->.*more than once/,
      ],
      ['no html start tag', TEMPLATE.replace('<html lang="xx" class="page">', ''), /<html>/],
    ]

    it.each(cases)('with %s', async (_name, template, message) => {
      const render = vi.fn(stubRender)

      await expect(localizedFiles(ROUTES, { origin: ORIGIN, template, render })).rejects.toThrow(
        message,
      )
      expect(render).not.toHaveBeenCalled()
    })

    it('with a duplicate route path', async () => {
      const render = vi.fn(stubRender)
      const routes = [
        { path: '/impressum', public: true },
        { path: '/impressum', public: true },
      ]

      await expect(
        localizedFiles(routes, { origin: ORIGIN, template: TEMPLATE, render }),
      ).rejects.toThrow(/duplicate path/)
      expect(render).not.toHaveBeenCalled()
    })

    it('with an origin that is not https://host', async () => {
      const render = vi.fn(stubRender)

      await expect(
        localizedFiles(ROUTES, { origin: 'https://example.com/', template: TEMPLATE, render }),
      ).rejects.toThrow(/govue origin/)
      expect(render).not.toHaveBeenCalled()
    })

    it('without a public route', async () => {
      const render = vi.fn(stubRender)

      await expect(
        localizedFiles([{ path: '/app' }], { origin: ORIGIN, template: TEMPLATE, render }),
      ).rejects.toThrow(/no public route/)
      expect(render).not.toHaveBeenCalled()
    })
  })

  it('rejects when render resolves to something other than a string', async () => {
    await expect(
      localizedFiles(ROUTES, {
        origin: ORIGIN,
        template: TEMPLATE,
        render: () => Promise.resolve(42 as unknown as string),
      }),
    ).rejects.toThrow(/"\/de\/".*must resolve to a string/)
  })

  it('rejects when render rejects, never swallowing the error', async () => {
    await expect(
      localizedFiles(ROUTES, {
        origin: ORIGIN,
        template: TEMPLATE,
        render: () => Promise.reject(new Error('boom')),
      }),
    ).rejects.toThrow('boom')
  })
})

describe('setHtmlLang', () => {
  const cases: [string, string, string][] = [
    ['replaces a quoted lang', '<html lang="en">', '<html lang="de">'],
    ['replaces a single quoted lang', "<html lang='en'>", '<html lang="de">'],
    ['replaces an unquoted lang', '<html lang=en class=a>', '<html lang="de" class=a>'],
    ['matches the tag and attribute in any case', '<HTML LANG="en">', '<html lang="de">'],
    ['adds a missing lang', '<html>', '<html lang="de">'],
    ['keeps other attributes in order', '<html a="1" lang="en" b>', '<html lang="de" a="1" b>'],
    ['drops every lang', '<html lang="en" lang="fr">', '<html lang="de">'],
    [
      'keeps a data-lang and an xml:lang',
      '<html data-lang="x" xml:lang="en">',
      '<html lang="de" data-lang="x" xml:lang="en">',
    ],
    ['spans lines', '<html\n  lang="en"\n  class="a"\n>', '<html lang="de" class="a">'],
    [
      'keeps a ">" inside a quoted value',
      '<html title="a>b" lang="en">',
      '<html lang="de" title="a>b">',
    ],
    ['keeps the doctype', '<!doctype html><html lang="en">', '<!doctype html><html lang="de">'],
    [
      'skips a commented tag',
      '<!-- <html lang="en"> --><html>',
      '<!-- <html lang="en"> --><html lang="de">',
    ],
    ['ignores a longer tag name', '<htmlx lang="en"><html>', '<htmlx lang="en"><html lang="de">'],
  ]

  it.each(cases)('%s', (_name, input, expected) => {
    expect(setHtmlLang(input, 'de')).toBe(expected)
  })

  it('throws without an html start tag', () => {
    expect(() => setHtmlLang('<head></head>', 'de')).toThrow(/<html>/)
    expect(() => setHtmlLang('<!-- <html> -->', 'de')).toThrow(/<html>/)
  })

  it('throws for two html start tags', () => {
    expect(() => setHtmlLang('<html><html>', 'de')).toThrow(/more than once/)
  })

  it('throws for an unterminated html start tag', () => {
    expect(() => setHtmlLang('<html lang="en"', 'de')).toThrow(/not closed/)
  })
})
