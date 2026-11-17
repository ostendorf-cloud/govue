// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { render, screen } from '@testing-library/vue'
import { describe, expect, it } from 'vitest'

import { createGovueI18n } from '../i18n/plugin'
import { exampleProvider } from '../legal/example/exampleProvider'
import { createGovueLegal } from '../legal/provider'
import type { LegalProvider } from '../legal/types'
import GovueImprintPage from './GovueImprintPage.vue'

function mount(locale: string, props: Record<string, unknown> = {}, provider = exampleProvider) {
  return render(GovueImprintPage, {
    props,
    global: { plugins: [createGovueI18n({ locale }), createGovueLegal(provider)] },
  })
}

describe('GovueImprintPage', () => {
  it('renders the imprint from the provider in German', () => {
    mount('de')

    expect(screen.getByRole('heading', { level: 1, name: 'Impressum' })).toBeVisible()
    expect(screen.getByText(/Example Provider/)).toBeVisible()
    expect(screen.getByText(/1 Example Street/)).toBeVisible()
    expect(screen.getByRole('heading', { name: 'Kontakt' })).toBeVisible()
    expect(screen.getByRole('heading', { name: 'Umsatzsteuer-ID' })).toBeVisible()
    expect(screen.getByText(/gemäß §27a Umsatzsteuergesetz: DE000000000/)).toBeVisible()
    expect(screen.getByRole('heading', { name: 'Verbraucherstreitbeilegung' })).toBeVisible()
    expect(screen.getByText(/weder verpflichtet noch bereit/)).toBeVisible()
  })

  it('renders the imprint from the provider in English', () => {
    mount('en')

    expect(screen.getByRole('heading', { level: 1, name: 'Imprint' })).toBeVisible()
    expect(screen.getByRole('heading', { name: 'Contact' })).toBeVisible()
    expect(screen.getByRole('heading', { name: 'VAT ID' })).toBeVisible()
    expect(screen.getByText(/per Section 27a of the German VAT Act: DE000000000/)).toBeVisible()
    expect(screen.getByRole('heading', { name: 'Consumer dispute resolution' })).toBeVisible()
    expect(screen.getByText(/neither obliged nor willing/)).toBeVisible()
  })

  it('writes the country in the page language', () => {
    mount('de')
    expect(screen.getByText(/00000 Example City, Deutschland/)).toBeVisible()
  })

  it('writes the country in English on the English page', () => {
    mount('en')
    expect(screen.getByText(/00000 Example City, Germany/)).toBeVisible()
  })

  it('links the phone and the email', () => {
    mount('de')

    expect(screen.getByRole('link', { name: '+49 000 0000000' })).toHaveAttribute(
      'href',
      'tel:+490000000000',
    )
    expect(screen.getByRole('link', { name: 'legal@example.invalid' })).toHaveAttribute(
      'href',
      'mailto:legal@example.invalid',
    )
  })

  it('shows what the provider says and not a fixed text', () => {
    const other: LegalProvider = {
      ...exampleProvider,
      name: 'Other Provider',
      vatId: 'DE111111111',
      disputeResolution: {
        de: 'Wir nehmen teil.',
        en: 'We take part.',
      },
    }
    mount('en', {}, other)

    expect(screen.getByText(/Other Provider/)).toBeVisible()
    expect(screen.getByText(/DE111111111/)).toBeVisible()
    expect(screen.getByText('We take part.')).toBeVisible()
    expect(screen.queryByText(/Example Provider/)).toBeNull()
  })

  it('takes a provider prop over the installed one', () => {
    render(GovueImprintPage, {
      props: { provider: { ...exampleProvider, name: 'Prop Provider' } },
      global: { plugins: [createGovueI18n({ locale: 'de' }), createGovueLegal(exampleProvider)] },
    })

    expect(screen.getByText(/Prop Provider/)).toBeVisible()
  })

  it('marks the page and its language for the site check', () => {
    const view = mount('en')

    const marker = view.container.querySelector('[data-govue-legal="imprint"]')
    expect(marker).toHaveAttribute('lang', 'en')
  })

  it('marks the German page as German', () => {
    const view = mount('de')

    expect(view.container.querySelector('[data-govue-legal="imprint"]')).toHaveAttribute(
      'lang',
      'de',
    )
  })

  it('says the German text is binding on the English page, with a link to it', () => {
    mount('en', { germanHref: '/de/imprint' })

    expect(screen.getByText(/The German version is legally binding/)).toBeVisible()
    expect(screen.getByRole('link', { name: 'View the German version' })).toHaveAttribute(
      'href',
      '/de/imprint',
    )
  })

  it('shows no binding notice on the German page', () => {
    mount('de', { germanHref: '/de/imprint' })

    expect(screen.queryByText(/rechtlich verbindlich/)).toBeNull()
    expect(screen.queryByRole('link', { name: 'Deutsche Fassung ansehen' })).toBeNull()
  })

  it('shows no link when the site gives no German address', () => {
    mount('en')

    expect(screen.queryByRole('link', { name: 'View the German version' })).toBeNull()
  })

  it('throws a setup error when no provider was installed', () => {
    expect(() =>
      render(GovueImprintPage, { global: { plugins: [createGovueI18n({ locale: 'de' })] } }),
    ).toThrow('createGovueLegal')
  })
})
