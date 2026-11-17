// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { render } from '@testing-library/vue'
import { describe, expect, it } from 'vitest'

import { localeCodes } from '../../i18n/catalogues'
import { createGovueI18n } from '../../i18n/plugin'
import { createGovueLegal } from '../provider'
import ExampleImprint from './ExampleImprint.vue'
import ExamplePrivacy from './ExamplePrivacy.vue'
import { exampleProvider } from './exampleProvider'

const pages = { imprint: ExampleImprint, privacy: ExamplePrivacy } as const

describe('the example site', () => {
  for (const kind of ['imprint', 'privacy'] as const) {
    for (const code of localeCodes) {
      it(`carries the ${kind} page in ${code}`, () => {
        const view = render(pages[kind], {
          global: {
            plugins: [createGovueI18n({ locale: code }), createGovueLegal(exampleProvider)],
          },
        })

        const marker = view.container.querySelector(`[data-govue-legal="${kind}"]`)
        expect(marker).toHaveAttribute('lang', code)
        expect(marker?.textContent?.length).toBeGreaterThan(200)
      })
    }
  }

  it('uses every optional block and a section of its own on the privacy page', () => {
    const view = render(ExamplePrivacy, {
      global: { plugins: [createGovueI18n({ locale: 'en' }), createGovueLegal(exampleProvider)] },
    })

    for (const id of ['session-cookie', 'server-logs', 'rate-limit', 'umami', 'contact-form']) {
      expect(view.container.querySelector(`#${id}`), id).not.toBeNull()
    }
    expect(view.container.querySelector('#example-newsletter')).not.toBeNull()
  })

  it('carries placeholder provider data only', () => {
    const all = JSON.stringify(exampleProvider)

    expect(all).toContain('Example Provider')
    expect(all).toContain('DE000000000')
    expect(all).toContain('example.invalid')
  })
})
