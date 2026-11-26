// @vitest-environment node
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { afterEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'

import { createClient } from '../api/client'
import { render } from '../entry-server'
import { createGovueI18n } from '../i18n/plugin'
import GovueWaitlistForm from './GovueWaitlistForm.vue'

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

async function html(props: Record<string, unknown>, locale = 'en'): Promise<string> {
  const Page = defineComponent({
    setup: () => () => h(GovueWaitlistForm, { client: createClient(), ...props }),
  })
  return render(Page, {
    setup: (app) => {
      app.use(createGovueI18n({ locale }))
    },
  })
}

describe('GovueWaitlistForm on the server', () => {
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

    const page = await html({
      client: createClient({ fetch: () => trap('fetch') }),
      privacyHref: '/en/privacy',
      consentLabel: 'I agree to receive the launch mail.',
    })

    expect(touched).toEqual([])
    expect(page).toContain('<form')
    expect(page).toContain('Join the waitlist')
    expect(page).toContain('href="/en/privacy"')
    expect(page).toContain('name="website"')
    expect(page).toContain('role="status"')
  })

  it('says in the page that the form needs JavaScript, and renders the button disabled', async () => {
    const page = await html({ consentLabel: 'I agree.' })

    expect(page).toMatch(/<noscript>[\s\S]*This form needs JavaScript[\s\S]*<\/noscript>/)
    // Without JavaScript the button stays disabled, so a native submit never
    // puts the address into a query string.
    expect(page).toMatch(/<button[^>]*type="submit"[^>]*disabled/)
  })

  it('renders an alert and no field when the consent text is missing', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => undefined)
    const page = await html({})
    expect(page).toContain('The waitlist form is not available.')
    expect(page).not.toContain('<form')
    expect(page).not.toContain('<input')
  })

  it('renders in German', async () => {
    const page = await html({ consentLabel: 'Ich stimme zu.' }, 'de')
    expect(page).toContain('Auf die Warteliste')
    expect(page).toContain('Warteliste')
  })
})
