// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { render, screen } from '@testing-library/vue'
import { describe, expect, it } from 'vitest'

import { createGovueI18n } from '../i18n/plugin'
import { serverLogs } from '../legal/blocks'
import { exampleProvider } from '../legal/example/exampleProvider'
import { p as legalParagraph, section as legalSection } from '../legal/helpers'
import { createGovueLegal } from '../legal/provider'
import GovuePrivacyPage from './GovuePrivacyPage.vue'

function mount(
  locale: string,
  options: { props?: Record<string, unknown>; slots?: Record<string, string> } = {},
) {
  return render(GovuePrivacyPage, {
    props: options.props ?? {},
    slots: options.slots ?? {},
    global: { plugins: [createGovueI18n({ locale }), createGovueLegal(exampleProvider)] },
  })
}

describe('GovuePrivacyPage', () => {
  it('renders the shared parts in German', () => {
    mount('de')

    expect(screen.getByRole('heading', { level: 1, name: 'Datenschutzerklärung' })).toBeVisible()
    for (const name of [
      '1. Datenschutz auf einen Blick',
      '2. Hosting',
      '3. Allgemeine Hinweise und Pflichtinformationen',
      'Widerspruch gegen Werbe-E-Mails',
      'Recht auf Datenübertragbarkeit',
    ]) {
      expect(screen.getByRole('heading', { name })).toBeVisible()
    }
  })

  it('renders the shared parts in English', () => {
    mount('en')

    expect(screen.getByRole('heading', { level: 1, name: 'Privacy policy' })).toBeVisible()
    for (const name of [
      '1. Data protection at a glance',
      '2. Hosting',
      '3. General notes and mandatory information',
      'Objection to promotional emails',
      'Right to data portability',
    ]) {
      expect(screen.getByRole('heading', { name })).toBeVisible()
    }
  })

  it('has the rights of the data subject', () => {
    mount('en')

    for (const name of [
      'Access, rectification and erasure',
      'Right to restriction of processing',
      'Right to lodge a complaint with the competent supervisory authority',
    ]) {
      expect(screen.getByRole('heading', { name })).toBeVisible()
    }
  })

  it('names the controller from the provider configuration', () => {
    const view = mount('en')

    const controller = view.container.querySelector('#responsible')
    expect(controller).toHaveTextContent('Example Provider')
    expect(controller).toHaveTextContent('1 Example Street')
    expect(controller).toHaveTextContent('00000 Example City, Germany')
    expect(controller).toHaveTextContent('+49 000 0000000')
    expect(controller).toHaveTextContent('legal@example.invalid')
  })

  it('shows part 4 only when the site adds sections', () => {
    mount('en')

    expect(
      screen.queryByRole('heading', { name: /Data collection on this website$/, level: 2 }),
    ).toBeNull()
  })

  it('renders the section list of the site under part 4, with the blocks', () => {
    mount('en', {
      props: {
        sections: [
          serverLogs(),
          legalSection('own', ['Eigen', 'Own section'], [legalParagraph('Text.', 'Own text.')]),
        ],
      },
    })

    expect(
      screen.getByRole('heading', { name: '4. Data collection on this website' }),
    ).toBeVisible()
    expect(screen.getByRole('heading', { name: 'Server log files' })).toBeVisible()
    expect(screen.getByRole('heading', { name: 'Own section' })).toBeVisible()
    expect(screen.getByText('Own text.')).toBeVisible()
  })

  it('renders the site sections in German on the German page', () => {
    mount('de', { props: { sections: [serverLogs()] } })

    expect(
      screen.getByRole('heading', { name: '4. Datenerfassung auf dieser Website' }),
    ).toBeVisible()
    expect(screen.getByRole('heading', { name: 'Server-Log-Dateien' })).toBeVisible()
  })

  it('takes site content through the slots', () => {
    mount('en', {
      slots: {
        intro: 'Intro of the site.',
        glance: '<span>Glance addition.</span>',
        default: '<span>Part four content.</span>',
        extra: '<span>5. Social media</span>',
      },
    })

    expect(screen.getByText('Intro of the site.')).toBeVisible()
    expect(screen.getByText('Glance addition.')).toBeVisible()
    expect(screen.getByText('Part four content.')).toBeVisible()
    expect(screen.getByText('5. Social media')).toBeVisible()
    expect(
      screen.getByRole('heading', { name: '4. Data collection on this website' }),
    ).toBeVisible()
  })

  it('marks the page and its language for the site check', () => {
    const view = mount('de')

    expect(view.container.querySelector('[data-govue-legal="privacy"]')).toHaveAttribute(
      'lang',
      'de',
    )
  })

  it('says the German text is binding on the English page, with a link to it', () => {
    mount('en', { props: { germanHref: '/de/privacy' } })

    expect(screen.getByText(/The German version is legally binding/)).toBeVisible()
    expect(screen.getByRole('link', { name: 'View the German version' })).toHaveAttribute(
      'href',
      '/de/privacy',
    )
  })

  it('shows no binding notice on the German page', () => {
    mount('de')

    expect(screen.queryByText(/rechtlich verbindlich/)).toBeNull()
  })

  it('sets the Art. 21 notices in capitals', () => {
    const view = mount('de')

    expect(view.container.querySelectorAll('p.uppercase')).toHaveLength(2)
  })
})
