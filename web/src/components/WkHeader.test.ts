// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { render, screen } from '@testing-library/vue'
import { describe, expect, it } from 'vitest'
import { h } from 'vue'

import WkHeader from './WkHeader.vue'

function renderHeader() {
  return render(WkHeader, {
    slots: {
      brand: () => h('a', { href: '/' }, 'Home'),
      default: () => h('nav', { 'aria-label': 'Main' }, h('a', { href: '/projects' }, 'Projects')),
      actions: () => h('button', { type: 'button' }, 'Sign out'),
    },
  })
}

describe('WkHeader', () => {
  it('is the banner landmark', () => {
    renderHeader()

    expect(screen.getByRole('banner')).toBeVisible()
  })

  it('shows the brand, the navigation and the actions', () => {
    renderHeader()

    expect(screen.getByRole('link', { name: 'Home' })).toBeVisible()
    expect(screen.getByRole('navigation', { name: 'Main' })).toBeVisible()
    expect(screen.getByRole('button', { name: 'Sign out' })).toBeVisible()
  })

  it('places the brand, the navigation and the actions as real, focusable controls in document order', () => {
    renderHeader()

    const brand = screen.getByRole('link', { name: 'Home' })
    const navLink = screen.getByRole('link', { name: 'Projects' })
    const action = screen.getByRole('button', { name: 'Sign out' })

    // Each one has to be a genuinely focusable element, never pulled out of
    // the tab order, since userEvent.tab() is not available to prove this by
    // simulating a real traversal.
    for (const element of [brand, navLink, action]) {
      expect(element).not.toHaveAttribute('tabindex', '-1')
      expect(element).not.toBeDisabled()
    }

    // The DOM order matches the visual order on a wide screen: order-1/2/3
    // only changes the layout below 480px, never the markup order.
    expect(Boolean(brand.compareDocumentPosition(navLink) & Node.DOCUMENT_POSITION_FOLLOWING)).toBe(
      true,
    )
    expect(
      Boolean(navLink.compareDocumentPosition(action) & Node.DOCUMENT_POSITION_FOLLOWING),
    ).toBe(true)

    // And each one is directly reachable.
    brand.focus()
    expect(brand).toHaveFocus()
    navLink.focus()
    expect(navLink).toHaveFocus()
    action.focus()
    expect(action).toHaveFocus()
  })

  it('wraps to two rows on a narrow screen instead of scrolling sideways', () => {
    renderHeader()

    const row = screen.getByRole('banner').firstElementChild
    expect(row?.className).toContain('flex-wrap')
    expect(row?.className).toContain('min-[480px]:flex-nowrap')
    expect(row?.className).toContain('min-[480px]:h-header')
  })
})
