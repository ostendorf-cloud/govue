// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { render, screen } from '@testing-library/vue'
import { describe, expect, it } from 'vitest'

import GovueCard from './GovueCard.vue'

describe('GovueCard', () => {
  it('shows what it was given', () => {
    render(GovueCard, { slots: { default: 'Four projects, two of them shared' } })

    expect(screen.getByText('Four projects, two of them shared')).toBeVisible()
  })

  it('renders a title as a second level heading by default', () => {
    render(GovueCard, { props: { title: 'Recent activity' }, slots: { default: 'Body' } })

    expect(screen.getByRole('heading', { level: 2, name: 'Recent activity' })).toBeVisible()
  })

  it.each([3, 4, 5, 6] as const)('renders the title at level %i when asked to', (level) => {
    render(GovueCard, {
      props: { title: 'Recent activity', headingLevel: level },
      slots: { default: 'Body' },
    })

    expect(screen.getByRole('heading', { level, name: 'Recent activity' })).toBeVisible()
  })

  it.each([0, 1, NaN, 2.5, '3', null, 7])(
    'falls back to a second level heading for %j, which is not a heading level',
    (level) => {
      render(GovueCard, {
        // The types refuse these values, the runtime still copes.
        props: { title: 'Recent activity', headingLevel: level as never },
        slots: { default: 'Body' },
      })

      expect(screen.getByRole('heading', { level: 2, name: 'Recent activity' })).toBeVisible()
    },
  )

  it('is a plain surface by default, not a part of the page a reader can jump to', () => {
    render(GovueCard, { props: { title: 'Recent activity' }, slots: { default: 'Body' } })

    expect(screen.queryByRole('article')).toBeNull()
    expect(screen.queryByRole('region')).toBeNull()
  })

  it('becomes a named part of the page when it is asked to', () => {
    render(GovueCard, {
      props: { as: 'article', title: 'Recent activity' },
      slots: { default: 'Body' },
    })

    expect(screen.getByRole('article', { name: 'Recent activity' })).toBeVisible()
  })

  it('shows the header and the footer slots', () => {
    render(GovueCard, {
      slots: { header: 'Two shared', default: 'Body', footer: 'Updated today' },
    })

    expect(screen.getByText('Two shared')).toBeVisible()
    expect(screen.getByText('Updated today')).toBeVisible()
  })

  it('leaves out the footer nobody filled', () => {
    render(GovueCard, { slots: { default: 'Body' } })

    expect(screen.queryByText('Updated today')).toBeNull()
  })
})
