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

  it('renders a title as a third level heading', () => {
    render(GovueCard, { props: { title: 'Recent activity' }, slots: { default: 'Body' } })

    expect(screen.getByRole('heading', { level: 3, name: 'Recent activity' })).toBeVisible()
  })

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
