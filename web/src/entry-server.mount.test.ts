// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, defineComponent, h } from 'vue'

import GovueLanguageSwitch from './components/GovueLanguageSwitch.vue'
import GovueThemeSwitch from './components/GovueThemeSwitch.vue'
import { resetThemeForTests, useTheme } from './composables/useTheme'
import { THEME_STORAGE_KEY } from './composables/themeStorage'
import { render } from './entry-server'
import { createGovueI18n } from './i18n/plugin'

const Page = defineComponent({
  setup() {
    useTheme()
    return () => h('main', [h(GovueThemeSwitch), h(GovueLanguageSwitch)])
  },
})

function checked(container: Element): string[] {
  return [...container.querySelectorAll('[role="radio"]')]
    .filter((node) => node.getAttribute('aria-checked') === 'true')
    .map((node) => node.textContent.trim())
}

// The documented way to start the client over a prerendered page: createApp,
// which replaces the markup, never createSSRApp, which would keep markup that
// was rendered before the visitor's stored choice and browser language were
// known.
describe('mounting over prerendered markup', () => {
  let warn: ReturnType<typeof vi.spyOn>

  beforeEach(() => {
    resetThemeForTests()
    localStorage.clear()
    warn = vi.spyOn(console, 'warn').mockImplementation(() => undefined)
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('shows the visitor choice after createApp, with no mismatch warning', async () => {
    // The build has no visitor: system mode, English.
    const html = await render(Page, {
      setup: (app) => {
        app.use(createGovueI18n({ locale: 'en' }))
      },
    })
    const root = document.createElement('div')
    root.innerHTML = html
    document.body.append(root)
    const before = checked(root)

    // The visitor stored dark mode and reads German.
    localStorage.setItem(THEME_STORAGE_KEY, JSON.stringify({ theme: 'sample', mode: 'dark' }))
    vi.spyOn(navigator, 'languages', 'get').mockReturnValue(['de'])
    resetThemeForTests()
    const app = createApp(Page)
    app.use(createGovueI18n())
    app.mount(root)

    expect(before).not.toEqual(checked(root))
    expect(checked(root)).toHaveLength(1)
    expect(root.querySelector('select')?.value).toBe('de')
    expect(warn).not.toHaveBeenCalled()
    app.unmount()
    root.remove()
  })
})
