// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { fireEvent, render, screen } from '@testing-library/vue'
import { describe, expect, it } from 'vitest'

import GovueNav from './GovueNav.vue'

const items = [
  { href: '#projects', label: 'Projects' },
  { href: '#settings', label: 'Settings', current: true },
]

function renderNav(props: Record<string, unknown> = {}) {
  return render(GovueNav, { props: { items, ...props } })
}

describe('GovueNav', () => {
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

  describe('call to action', () => {
    const withCta = [
      { href: '#projects', label: 'Projects' },
      { href: '#contact', label: 'Contact me', variant: 'cta' as const },
    ]

    it('renders the item as a button from the role tokens, still a link', () => {
      renderNav({ items: withCta })

      const cta = screen.getByRole('link', { name: 'Contact me' })
      expect(cta).toHaveAttribute('href', '#contact')
      expect(cta).toHaveClass('bg-accent-strong', 'text-accent-on', 'hover:bg-accent-strong-hover')
      expect(cta).toHaveAttribute('data-variant', 'cta')
      // A plain item next to it is untouched.
      const plain = screen.getByRole('link', { name: 'Projects' })
      expect(plain).not.toHaveClass('bg-accent-strong')
      expect(plain).not.toHaveAttribute('data-variant')
    })

    it('has a 44px target in the row and in the open menu', async () => {
      renderNav({ items: withCta })

      const cta = screen.getByRole('link', { name: 'Contact me' })
      expect(cta).toHaveClass('min-h-11')
      // The same element is the menu entry below sm: full width, same height.
      expect(cta).toHaveClass('w-full', 'sm:w-auto')
      await fireEvent.click(screen.getByRole('button', { name: 'Menu' }))
      expect(screen.getByRole('list').className).not.toContain('hidden')
      expect(screen.getByRole('link', { name: 'Contact me' })).toHaveClass('min-h-11')
    })

    it('names no colour and no focus ring of its own', () => {
      renderNav({ items: withCta })

      const className = screen.getByRole('link', { name: 'Contact me' }).className
      expect(className).not.toMatch(/#[0-9a-f]{3,6}|rgb|outline|ring/i)
    })

    it('says the current page by aria-current and an underline, not by the fill', () => {
      renderNav({ items: [{ href: '#c', label: 'Contact', variant: 'cta', current: true }] })

      const cta = screen.getByRole('link', { name: 'Contact' })
      expect(cta).toHaveAttribute('aria-current', 'page')
      expect(cta).toHaveClass('underline')
      expect(cta).toHaveClass('bg-accent-strong')
    })

    it('is reachable and closes the open menu when followed', async () => {
      renderNav({ items: withCta })
      const toggle = screen.getByRole('button', { name: 'Menu' })

      await fireEvent.click(toggle)
      const cta = screen.getByRole('link', { name: 'Contact me' })
      cta.focus()
      expect(cta).toHaveFocus()
      await fireEvent.click(cta)

      expect(toggle).toHaveAttribute('aria-expanded', 'false')
    })

    it('leaves the look of an item without a variant unchanged', () => {
      renderNav()

      expect(screen.getByRole('link', { name: 'Projects' }).className).toBe(
        'inline-flex min-h-11 w-full items-center rounded-md px-3 text-base transition-colors sm:w-auto text-text hover:bg-surface-hover',
      )
    })
  })

  it('takes its own label for the toggle', () => {
    renderNav({ toggleLabel: 'Sections' })

    expect(screen.getByRole('button', { name: 'Sections' })).toBeVisible()
  })
})
