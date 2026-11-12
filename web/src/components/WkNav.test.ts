// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { fireEvent, render, screen } from '@testing-library/vue'
import { describe, expect, it } from 'vitest'

import WkNav from './WkNav.vue'

const items = [
  { href: '#projects', label: 'Projects' },
  { href: '#settings', label: 'Settings', current: true },
]

function renderNav(props: Record<string, unknown> = {}) {
  return render(WkNav, { props: { items, ...props } })
}

describe('WkNav', () => {
  it('is a navigation landmark named Main by default', () => {
    renderNav()

    expect(screen.getByRole('navigation', { name: 'Main' })).toBeVisible()
  })

  it('takes a name, so a second navigation is told apart', () => {
    renderNav({ ariaLabel: 'Footer' })

    expect(screen.getByRole('navigation', { name: 'Footer' })).toBeVisible()
  })

  it('renders every item as a link to its destination', () => {
    renderNav()

    expect(screen.getByRole('link', { name: 'Projects' })).toHaveAttribute('href', '#projects')
    expect(screen.getByRole('link', { name: 'Settings' })).toHaveAttribute('href', '#settings')
  })

  it('marks the current page, and not with colour alone', () => {
    renderNav()

    const current = screen.getByRole('link', { name: 'Settings' })
    expect(current).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('link', { name: 'Projects' })).not.toHaveAttribute('aria-current')
    // Weight and an underline carry the state as well, for anyone who does not
    // see the colour difference.
    expect(current.className).toContain('font-semibold')
    expect(current.className).toContain('underline')
  })

  it('starts collapsed and says which list the toggle controls', () => {
    renderNav()

    const toggle = screen.getByRole('button', { name: 'Menu' })
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(toggle).toHaveAttribute('aria-controls', screen.getByRole('list').id)
  })

  it('opens and closes on the toggle', async () => {
    renderNav()

    const toggle = screen.getByRole('button', { name: 'Menu' })
    await fireEvent.click(toggle)
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    // display:none below sm is what hides it; the class is the only trace of
    // that in a test environment without the stylesheet.
    expect(screen.getByRole('list').className).not.toContain('hidden')

    await fireEvent.click(toggle)
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(screen.getByRole('list').className).toContain('hidden')
  })

  it('closes on Escape and puts focus back on the toggle', async () => {
    renderNav()

    const toggle = screen.getByRole('button', { name: 'Menu' })
    await fireEvent.click(toggle)

    // Focus a link directly rather than simulating a real Tab traversal, then
    // prove Escape, dispatched from wherever focus sits, bubbles up to the
    // nav's own listener and returns focus to the toggle.
    const firstLink = screen.getByRole('link', { name: 'Projects' })
    firstLink.focus()
    expect(firstLink).toHaveFocus()

    await fireEvent.keyDown(firstLink, { key: 'Escape' })

    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(toggle).toHaveFocus()
  })

  it('closes when a link is followed', async () => {
    renderNav()

    const toggle = screen.getByRole('button', { name: 'Menu' })
    await fireEvent.click(toggle)
    await fireEvent.click(screen.getByRole('link', { name: 'Projects' }))

    expect(toggle).toHaveAttribute('aria-expanded', 'false')
  })

  it('takes its own label for the toggle', () => {
    renderNav({ toggleLabel: 'Sections' })

    expect(screen.getByRole('button', { name: 'Sections' })).toBeVisible()
  })
})
