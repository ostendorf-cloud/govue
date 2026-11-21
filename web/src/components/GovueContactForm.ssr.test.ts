// @vitest-environment node
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { afterEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'

import { createClient } from '../api/client'
import { render } from '../entry-server'
import { createGovueI18n } from '../i18n/plugin'
import GovueContactForm from './GovueContactForm.vue'

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('GovueContactForm on the server', () => {
  it('renders without touching Worker, crypto.subtle or fetch', async () => {
    const touched: string[] = []
    const trap = (name: string) => {
      touched.push(name)
      throw new Error(`${name} touched during the server render`)
    }
    vi.stubGlobal('Worker', function Worker() {
      trap('Worker')
    })
    vi.stubGlobal('crypto', {
      get subtle() {
        return trap('crypto.subtle')
      },
    })
    vi.stubGlobal('fetch', () => trap('fetch'))
    // There is no document in this environment, so no cookie can be read.
    expect(typeof document).toBe('undefined')

    const Page = defineComponent({
      setup: () => () =>
        h(GovueContactForm, {
          client: createClient({ fetch: () => trap('fetch') }),
          privacyHref: '/en/privacy',
          requireConsent: true,
          consentLabel: 'I agree to the test terms.',
        }),
    })
    const html = await render(Page, {
      setup: (app) => {
        app.use(createGovueI18n({ locale: 'en' }))
      },
    })

    expect(touched).toEqual([])
    expect(html).toContain('<form')
    expect(html).toContain('Send message')
    expect(html).toContain('href="/en/privacy"')
    expect(html).toContain('name="website"')
    expect(html).toContain('role="status"')
  })

  it('renders an alert and no field when the consent text is missing', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => undefined)
    const Page = defineComponent({
      setup: () => () => h(GovueContactForm, { client: createClient(), requireConsent: true }),
    })
    const html = await render(Page, {
      setup: (app) => {
        app.use(createGovueI18n({ locale: 'en' }))
      },
    })
    expect(html).toContain('role="alert"')
    expect(html).toContain('The contact form is not available.')
    expect(html).not.toContain('<form')
    expect(html).not.toContain('<input')
    expect(html).not.toContain('<textarea')
  })

  it('renders in German', async () => {
    const Page = defineComponent({
      setup: () => () => h(GovueContactForm, { client: createClient() }),
    })
    const html = await render(Page, {
      setup: (app) => {
        app.use(createGovueI18n({ locale: 'de' }))
      },
    })
    expect(html).toContain('Nachricht senden')
    expect(html).toContain('Kontakt')
  })
})
