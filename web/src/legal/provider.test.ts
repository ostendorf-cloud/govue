// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { render } from '@testing-library/vue'
import { describe, expect, it } from 'vitest'
import { defineComponent, h } from 'vue'

import { exampleProvider } from './example/exampleProvider'
import { assertProvider, createGovueLegal, useLegalProvider } from './provider'
import type { LegalProvider } from './types'

describe('assertProvider', () => {
  it('returns a complete provider unchanged', () => {
    expect(assertProvider(exampleProvider)).toBe(exampleProvider)
  })

  it.each(['name', 'street', 'postalCode', 'city', 'phone', 'email', 'vatId'] as const)(
    'throws naming %s when it is blank',
    (field) => {
      const broken: LegalProvider = { ...exampleProvider, [field]: '  ' }

      expect(() => assertProvider(broken)).toThrow(`${field} is empty`)
    },
  )

  it.each(['de', 'en'] as const)('throws when the dispute statement has no %s text', (code) => {
    const broken: LegalProvider = {
      ...exampleProvider,
      disputeResolution: { ...exampleProvider.disputeResolution, [code]: '' },
    }

    expect(() => assertProvider(broken)).toThrow(`no ${code} text`)
  })
})

describe('createGovueLegal', () => {
  const Probe = defineComponent({
    setup() {
      const provider = useLegalProvider()
      return () => h('p', provider.name)
    },
  })

  it('passes the provider to every page below the app', () => {
    const view = render(Probe, { global: { plugins: [createGovueLegal(exampleProvider)] } })

    expect(view.getByText('Example Provider')).toBeVisible()
  })

  it('refuses an incomplete provider at setup, not at render', () => {
    expect(() => createGovueLegal({ ...exampleProvider, vatId: '' })).toThrow('vatId is empty')
  })

  it('throws a setup error when no provider was installed', () => {
    expect(() => render(Probe)).toThrow('createGovueLegal')
  })
})
