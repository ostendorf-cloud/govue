// @vitest-environment node
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'
import { defineComponent, h } from 'vue'

import GovueButton from './components/GovueButton.vue'
import GovueCard from './components/GovueCard.vue'
import GovueFooter from './components/GovueFooter.vue'
import GovueHeader from './components/GovueHeader.vue'
import GovueLanguageSwitch from './components/GovueLanguageSwitch.vue'
import GovuePage from './components/GovuePage.vue'
import GovueSelect from './components/GovueSelect.vue'
import GovueThemeSwitch from './components/GovueThemeSwitch.vue'
import { resetThemeForTests, useTheme } from './composables/useTheme'
import { injectIntoTemplate, render } from './entry-server'
import { createGovueI18n } from './i18n/plugin'

describe('environment', () => {
  it('has no DOM, so a DOM dependent call path would throw', () => {
    expect(typeof window).toBe('undefined')
    expect(typeof document).toBe('undefined')
  })
})

describe('render', () => {
  it('renders a component to a string', async () => {
    const Page = defineComponent({
      setup: () => () => h(GovueButton, null, { default: () => 'Save' }),
    })
    const html = await render(Page)

    expect(html).toContain('<button')
    expect(html).toContain('Save')
  })

  it('passes props to the root component', async () => {
    const html = await render(GovueCard, { props: { title: 'Hello card' } })

    expect(html).toContain('Hello card')
  })

  it('lets the caller install plugins before rendering', async () => {
    const Page = defineComponent({
      setup: () => () => h(GovueLanguageSwitch),
    })
    const html = await render(Page, {
      setup: (app) => {
        app.use(createGovueI18n({ locale: 'en' }))
      },
    })

    expect(html).toContain('<select')
  })

  it('renders the overlay header transparent and the language links without script', async () => {
    const Page = defineComponent({
      setup: () => () => [
        h(GovueHeader, { overlay: true, navPlacement: 'end' }, { brand: () => 'Brand' }),
        h(GovueLanguageSwitch, { linkFor: (code: string) => `/${code}/about` }),
      ],
    })
    const html = await render(Page, {
      setup: (app) => {
        app.use(createGovueI18n({ locale: 'en' }))
      },
    })

    // No window on the server, so the state is the top of the page.
    expect(html).toContain('data-state="transparent"')
    expect(html).toContain('data-overlay="true"')
    expect(html).toContain('href="/de/about"')
    expect(html).toContain('hreflang="de"')
    expect(html).toContain('aria-current="true"')
    expect(html).not.toContain('<select')
  })

  it('renders a composed page with the theme switch and the language layer', async () => {
    const Page = defineComponent({
      setup() {
        useTheme({ theme: 'sample' })
        return () =>
          h(GovuePage, null, {
            default: () => [
              h(GovueHeader, { themeSwitch: true }, { brand: () => 'Brand' }),
              h(GovueLanguageSwitch),
              h(GovueSelect, { modelValue: 'a', options: [{ value: 'a', label: 'Alpha' }] }),
              h(GovueFooter),
            ],
          })
      },
    })
    const html = await render(Page, {
      setup: (app) => {
        app.use(createGovueI18n({ locale: 'de' }))
      },
    })

    expect(html).toContain('Brand')
    expect(html).toContain('<header')
    expect(html).toContain('<footer')
  })

  it('never touches localStorage without a window', async () => {
    // Newer Node versions define a global localStorage that warns, or throws,
    // on access. The theme state must not even look at it.
    resetThemeForTests()
    let reads = 0
    Object.defineProperty(globalThis, 'localStorage', {
      configurable: true,
      get() {
        reads++
        return undefined
      },
    })
    try {
      const Page = defineComponent({
        setup() {
          useTheme({ theme: 'sample' })
          return () => h(GovueThemeSwitch)
        },
      })
      await render(Page)
    } finally {
      Reflect.deleteProperty(globalThis, 'localStorage')
    }

    expect(reads).toBe(0)
  })

  it('renders the theme switch on its own', async () => {
    const html = await render(GovueThemeSwitch)

    expect(html).toContain('radio')
  })

  it('rejects when the root component throws, never swallowing the error', async () => {
    const Broken = defineComponent({
      setup() {
        throw new Error('boom')
      },
    })

    await expect(render(Broken)).rejects.toThrow('boom')
  })
})

describe('injectIntoTemplate', () => {
  const template = '<html><body><div id="app"><!--app-html--></div></body></html>'

  it('replaces the placeholder with the markup', () => {
    expect(injectIntoTemplate(template, '<p>x</p>')).toBe(
      '<html><body><div id="app"><p>x</p></div></body></html>',
    )
  })

  it('uses a custom placeholder', () => {
    expect(injectIntoTemplate('a{{ssr}}b', 'X', '{{ssr}}')).toBe('aXb')
  })

  it('does not interpret replacement patterns in the markup', () => {
    expect(injectIntoTemplate(template, '<p>$&</p>')).toContain('<p>$&</p>')
  })

  it('throws when the placeholder is missing', () => {
    expect(() => injectIntoTemplate('<html></html>', 'x')).toThrow(/placeholder/)
  })

  it('throws when the placeholder appears twice', () => {
    expect(() => injectIntoTemplate('<!--app-html--><!--app-html-->', 'x')).toThrow(
      /twice|more than once/,
    )
  })
})
