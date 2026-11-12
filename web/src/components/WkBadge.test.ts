// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { render, screen } from '@testing-library/vue'
import { describe, expect, it } from 'vitest'

import WkBadge from './WkBadge.vue'

describe('WkBadge', () => {
  it('says its state in words', () => {
    render(WkBadge, { props: { kind: 'danger' }, slots: { default: 'Overdue' } })

    // The word is the status, the colour only repeats it.
    expect(screen.getByText('Overdue')).toBeVisible()
  })

  it('is neutral unless it is given a kind', () => {
    render(WkBadge, { slots: { default: 'Draft' } })

    expect(screen.getByText('Draft').className).toContain('bg-bg-subtle')
  })

  it('pairs the tint of a kind with the text colour of the same kind', () => {
    render(WkBadge, { props: { kind: 'success' }, slots: { default: 'Paid' } })

    const badge = screen.getByText('Paid')
    expect(badge.className).toContain('bg-success-subtle')
    expect(badge.className).toContain('text-success-text')
  })

  it('carries no raw colour', () => {
    for (const kind of ['neutral', 'success', 'warning', 'danger', 'info'] as const) {
      const view = render(WkBadge, { props: { kind }, slots: { default: kind } })

      expect(screen.getByText(kind).className).not.toMatch(/#|\[rgb|dark:/)
      view.unmount()
    }
  })
})
