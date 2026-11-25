// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { fireEvent, render, screen } from '@testing-library/vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { h, nextTick } from 'vue'

import GovueHeader from './GovueHeader.vue'
import GovueNav from './GovueNav.vue'

/** setScroll moves the window and tells the page, as a browser does. */
function setScroll(y: number): void {
  Object.defineProperty(window, 'scrollY', { value: y, configurable: true, writable: true })
  window.dispatchEvent(new Event('scroll'))
}

function renderHeader() {
  return render(GovueHeader, {
    slots: {
      brand: () => h('a', { href: '/' }, 'Home'),
      default: () => h('nav', { 'aria-label': 'Main' }, h('a', { href: '/projects' }, 'Projects')),
      actions: () => h('button', { type: 'button' }, 'Sign out'),
    },
  })
}

describe('GovueHeader', () => {
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

  it('shows no theme switch unless asked', () => {
    renderHeader()

    expect(screen.queryByRole('radiogroup')).not.toBeInTheDocument()
  })

  it('shows the theme switch with the themeSwitch prop', () => {
    render(GovueHeader, { props: { themeSwitch: true } })

    expect(screen.getByRole('radiogroup', { name: 'Theme' })).toBeVisible()
  })

  it('lets a theme slot replace the switch', () => {
    render(GovueHeader, {
      props: { themeSwitch: true },
      slots: { theme: () => h('button', { type: 'button' }, 'Own toggle') },
    })

    expect(screen.getByRole('button', { name: 'Own toggle' })).toBeVisible()
    expect(screen.queryByRole('radiogroup')).not.toBeInTheDocument()
  })

  describe('ground', () => {
    it('keeps the look of a sticky solid bar by default', () => {
      renderHeader()

      const header = screen.getByRole('banner')
      expect(header).toHaveClass(
        'sticky',
        'top-0',
        'border-b',
        'bg-header-bg',
        'border-header-border',
      )
      expect(header).not.toHaveClass('fixed')
      expect(header).toHaveAttribute('data-state', 'solid')
      expect(header).not.toHaveAttribute('data-overlay')
    })

    it('uses the role tokens only, never a colour', () => {
      renderHeader()

      expect(screen.getByRole('banner').className).not.toMatch(/#|rgb|bg-surface|border-border\b/)
    })

    it('emits no blur of its own, so the default is no backdrop filter', () => {
      renderHeader()

      const className = screen.getByRole('banner').className
      // The filter comes from the token alone, and the token's default is
      // none (checked in tokens.test.ts). A blur(0px) would still make the
      // header the containing block of fixed descendants.
      expect(className).not.toMatch(/backdrop-blur|blur\(/)
      expect(className).toContain('[backdrop-filter:var(--header-backdrop)]')
    })

    it('sets the text colour, so slot content without its own follows the scheme', () => {
      renderHeader()

      expect(screen.getByRole('banner')).toHaveClass('text-text')
    })

    it('does not listen to the scroll unless it overlays', () => {
      const add = vi.spyOn(window, 'addEventListener')
      renderHeader()

      expect(add.mock.calls.some(([type]) => type === 'scroll')).toBe(false)
      add.mockRestore()
    })
  })

  describe('overlay', () => {
    beforeEach(() => setScroll(0))
    afterEach(() => setScroll(0))

    function renderOverlay() {
      return render(GovueHeader, {
        props: { overlay: true },
        slots: {
          brand: () => h('a', { href: '/' }, 'Home'),
          default: () => h(GovueNav, { items: [{ href: '/a', label: 'A' }] }),
        },
      })
    }

    it('leaves the flow and is transparent without border and blur at the top', () => {
      renderOverlay()

      const header = screen.getByRole('banner')
      expect(header).toHaveClass(
        'fixed',
        'inset-x-0',
        'top-0',
        'bg-transparent',
        'border-transparent',
      )
      expect(header).not.toHaveClass('sticky')
      expect(header).not.toHaveClass('bg-header-bg')
      expect(header.className).not.toContain('backdrop-filter')
      expect(header).toHaveAttribute('data-state', 'transparent')
      expect(header).toHaveAttribute('data-overlay', 'true')
    })

    it('shows the dark roles while transparent, for a dark hero', () => {
      renderOverlay()

      expect(screen.getByRole('banner').className).toContain('[color-scheme:dark]')
    })

    it('turns solid, blurred and bordered after the page scrolled, and back', async () => {
      renderOverlay()
      const header = screen.getByRole('banner')

      setScroll(200)
      await nextTick()
      expect(header).toHaveAttribute('data-state', 'solid')
      expect(header).toHaveClass('bg-header-bg', 'border-header-border')
      expect(header.className).toContain('[backdrop-filter:var(--header-backdrop)]')
      expect(header).not.toHaveClass('bg-transparent')
      expect(header.className).not.toContain('[color-scheme:dark]')

      setScroll(0)
      await nextTick()
      expect(header).toHaveAttribute('data-state', 'transparent')
    })

    it('stays out of the flow in both states, so nothing shifts when it turns solid', async () => {
      renderOverlay()
      const header = screen.getByRole('banner')
      const row = header.firstElementChild
      const border = (el: Element) => /\bborder-b\b/.test(el.className)

      const before = [header.className.includes('fixed'), border(header), row?.className]
      setScroll(200)
      await nextTick()

      expect([header.className.includes('fixed'), border(header), row?.className]).toEqual(before)
    })

    it('has a solid ground whenever the mobile menu is open', async () => {
      renderOverlay()
      const header = screen.getByRole('banner')
      expect(header).toHaveAttribute('data-state', 'transparent')

      await fireEvent.click(screen.getByRole('button', { name: 'Menu' }))
      expect(header).toHaveAttribute('data-state', 'solid')
      expect(header).toHaveClass('bg-header-bg')

      await fireEvent.click(screen.getByRole('button', { name: 'Menu' }))
      expect(header).toHaveAttribute('data-state', 'transparent')
    })

    it('goes transparent again when the open menu is closed by Escape', async () => {
      renderOverlay()
      const header = screen.getByRole('banner')

      await fireEvent.click(screen.getByRole('button', { name: 'Menu' }))
      await fireEvent.keyDown(screen.getByRole('link', { name: 'A' }), { key: 'Escape' })

      expect(header).toHaveAttribute('data-state', 'transparent')
    })

    it('switches the ground without a transition for reduced motion', () => {
      renderOverlay()

      const header = screen.getByRole('banner')
      expect(header).toHaveClass('transition-colors', 'motion-reduce:transition-none')
    })

    it('listens to the scroll passively, and only now', () => {
      const add = vi.spyOn(window, 'addEventListener')
      renderOverlay()

      const call = add.mock.calls.find(([type]) => type === 'scroll')
      expect(call?.[2]).toEqual({ passive: true })
      add.mockRestore()
    })

    it('starts solid when the page is already scrolled at mount', async () => {
      setScroll(800)
      renderOverlay()
      await nextTick()

      expect(screen.getByRole('banner')).toHaveAttribute('data-state', 'solid')
    })

    it('keeps the brand, navigation and actions reachable in the transparent state', () => {
      renderOverlay()

      screen.getByRole('link', { name: 'Home' }).focus()
      expect(screen.getByRole('link', { name: 'Home' })).toHaveFocus()
      expect(screen.getByRole('navigation', { name: 'Main' })).toBeVisible()
    })
  })

  describe('navPlacement', () => {
    function navWrapper(props: Record<string, unknown> = {}) {
      render(GovueHeader, {
        props,
        slots: { default: () => h('a', { href: '/x' }, 'Nav') },
      })
      return screen.getByRole('link', { name: 'Nav' }).parentElement as HTMLElement
    }

    it('keeps the navigation next to the brand by default', () => {
      const wrapper = navWrapper()

      expect(wrapper.className).not.toContain('justify-')
      expect(wrapper).toHaveClass('min-[480px]:flex-1')
    })

    it('puts the navigation in the centre', () => {
      expect(navWrapper({ navPlacement: 'center' })).toHaveClass('min-[480px]:justify-center')
    })

    it('puts the navigation at the end, next to the actions', () => {
      expect(navWrapper({ navPlacement: 'end' })).toHaveClass('min-[480px]:justify-end')
    })

    it('keeps the markup order whatever the placement', () => {
      render(GovueHeader, {
        props: { navPlacement: 'end' },
        slots: {
          brand: () => h('a', { href: '/' }, 'Home'),
          default: () => h('a', { href: '/x' }, 'Nav'),
          actions: () => h('button', { type: 'button' }, 'Act'),
        },
      })
      const nav = screen.getByRole('link', { name: 'Nav' })
      expect(
        Boolean(
          screen.getByRole('link', { name: 'Home' }).compareDocumentPosition(nav) &
          Node.DOCUMENT_POSITION_FOLLOWING,
        ),
      ).toBe(true)
      expect(
        Boolean(
          nav.compareDocumentPosition(screen.getByRole('button', { name: 'Act' })) &
          Node.DOCUMENT_POSITION_FOLLOWING,
        ),
      ).toBe(true)
    })
  })

  // jsdom has no layout and no media queries, so these pin the classes. The
  // e2e spec header-width.spec.ts measures the result in Chromium.
  describe('the open menu', () => {
    const DROP = 'min-[480px]:max-sm:'
    /** The classes of the list in the flow, the only ones below 480px and from
     *  640px. */
    const IN_FLOW_OPEN = ['gap-1', 'py-2', 'sm:flex', 'sm:flex-row', 'sm:items-center', 'sm:py-0']
    const DROPPED = [
      'absolute',
      'inset-x-0',
      'top-full',
      'max-h-[calc(100dvh-var(--spacing-header))]',
      'overflow-y-auto',
      'border-y',
      'border-header-border',
      'bg-header-bg',
      'px-4',
    ].map((c) => DROP + c)

    function renderWithNav(props: Record<string, unknown> = {}) {
      return render(GovueHeader, {
        props,
        slots: {
          brand: () => h('a', { href: '/' }, 'Home'),
          default: () =>
            h(GovueNav, {
              items: [
                { href: '#a', label: 'A' },
                { href: '#b', label: 'B', variant: 'cta' },
              ],
            }),
        },
      })
    }

    const classes = (el: Element) => el.className.split(/\s+/).filter(Boolean)

    it('drops below the bar between 480 and 639px and keeps the flow outside that range', async () => {
      renderWithNav()
      await fireEvent.click(screen.getByRole('button', { name: 'Menu' }))

      const list = classes(screen.getByRole('list'))
      expect(list.sort()).toEqual([...IN_FLOW_OPEN, 'flex', 'flex-col', ...DROPPED].sort())
      // Every class that is not the in flow set acts between 480 and 639px
      // only, so the second row below 480px and the inline row from 640px are
      // the same as before.
      expect(list.filter((c) => !c.startsWith(DROP)).sort()).toEqual(
        [...IN_FLOW_OPEN, 'flex', 'flex-col'].sort(),
      )
    })

    it('keeps the dropped classes while closed, hidden by display none', () => {
      renderWithNav()

      const list = classes(screen.getByRole('list'))
      expect(list).toContain('hidden')
      expect(list).not.toContain('flex')
      expect(list).toEqual(expect.arrayContaining(DROPPED))
    })

    it('positions the list against the header element and nothing in between', async () => {
      for (const overlay of [false, true]) {
        const { unmount } = renderWithNav({ overlay })
        await fireEvent.click(screen.getByRole('button', { name: 'Menu' }))

        const header = screen.getByRole('banner')
        expect(header).toHaveClass(overlay ? 'fixed' : 'sticky')
        let el = screen.getByRole('list').parentElement
        while (el && el !== header) {
          expect(el.className, el.tagName).not.toMatch(/\b(relative|absolute|fixed|sticky)\b/)
          el = el.parentElement
        }
        expect(el).toBe(header)
        unmount()
      }
    })

    it('leaves the header and its row as they are, so the height does not change', async () => {
      renderWithNav()
      const header = screen.getByRole('banner')
      const row = header.firstElementChild as HTMLElement
      const before = [header.className, row.className]

      await fireEvent.click(screen.getByRole('button', { name: 'Menu' }))

      expect([header.className, row.className]).toEqual(before)
      expect(row.className).toContain('min-[480px]:h-header')
    })

    it('keeps the menu button in the row, outside the list', async () => {
      renderWithNav()
      const toggle = screen.getByRole('button', { name: 'Menu' })
      await fireEvent.click(toggle)

      expect(screen.getByRole('list').contains(toggle)).toBe(false)
      expect(toggle).toHaveClass('sm:hidden')
      expect(toggle.className).not.toContain('max-sm:hidden')
    })

    it('closes on Escape with focus on the button, and on a followed link', async () => {
      renderWithNav()
      const toggle = screen.getByRole('button', { name: 'Menu' })

      await fireEvent.click(toggle)
      await fireEvent.keyDown(screen.getByRole('link', { name: 'A' }), { key: 'Escape' })
      expect(toggle).toHaveAttribute('aria-expanded', 'false')
      expect(toggle).toHaveFocus()

      await fireEvent.click(toggle)
      await fireEvent.click(screen.getByRole('link', { name: 'B' }))
      expect(toggle).toHaveAttribute('aria-expanded', 'false')
    })
  })

  it('wraps to two rows on a narrow screen instead of scrolling sideways', () => {
    renderHeader()

    const row = screen.getByRole('banner').firstElementChild
    expect(row?.className).toContain('flex-wrap')
    expect(row?.className).toContain('min-[480px]:flex-nowrap')
    expect(row?.className).toContain('min-[480px]:h-header')
  })
})
