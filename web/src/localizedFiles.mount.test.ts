// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, defineComponent, h } from 'vue'

import GovueLanguageSwitch from './components/GovueLanguageSwitch.vue'
import GovueSkipLink from './components/GovueSkipLink.vue'
import { render } from './entry-server'
import { localeFromPath } from './i18n/routes'
import { createGovueI18n } from './i18n/plugin'
import { localizedFiles } from './localizedFiles'

const TEMPLATE = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><!--app-head--></head>
<body><div id="app"><!--app-html--></div></body>
</html>
`

const Page = defineComponent({
  setup: () => () => h('main', [h(GovueSkipLink), h(GovueLanguageSwitch)]),
})

// The documented client start over a language page: createApp with the
// language of the URL, never createSSRApp.
describe('mounting over a prerendered language page', () => {
  let warn: ReturnType<typeof vi.spyOn>

  beforeEach(() => {
    localStorage.clear()
    warn = vi.spyOn(console, 'warn').mockImplementation(() => undefined)
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  const cases: [string, string, string][] = [
    ['/de/', 'de/index.html', 'Zum Inhalt springen'],
    ['/en/', 'en/index.html', 'Skip to content'],
  ]

  it.each(cases)('keeps %s in its language with no warning', async (url, file, text) => {
    const files = await localizedFiles([{ path: '/', public: true }], {
      origin: 'https://example.com',
      template: TEMPLATE,
      render: (variant) =>
        render(Page, {
          setup: (app) => {
            app.use(createGovueI18n({ locale: variant.locale }))
          },
        }),
    })
    const content = files.find((f) => f.path === file)?.content ?? ''
    const parsed = new DOMParser().parseFromString(content, 'text/html')
    const locale = localeFromPath(url)

    expect(parsed.documentElement.lang).toBe(locale)
    expect(parsed.querySelector('link[rel="canonical"]')?.getAttribute('href')).toBe(
      `https://example.com${url}`,
    )
    expect(
      [...parsed.querySelectorAll('link[rel="alternate"]')].map((l) => l.getAttribute('hreflang')),
    ).toEqual(['de', 'en', 'x-default'])

    // The visitor's browser reads the other language: the URL still wins.
    vi.spyOn(navigator, 'languages', 'get').mockReturnValue([locale === 'de' ? 'en' : 'de'])
    const root = document.createElement('div')
    root.innerHTML = parsed.getElementById('app')?.innerHTML ?? ''
    document.body.append(root)
    const before = root.textContent

    const app = createApp(Page)
    app.use(createGovueI18n({ locale }))
    app.mount(root)

    expect(before).toContain(text)
    expect(root.textContent).toBe(before)
    expect(root.querySelector('select')?.value).toBe(locale)
    expect(document.documentElement.lang).toBe(locale)
    expect(warn).not.toHaveBeenCalled()
    app.unmount()
    root.remove()
  })
})
