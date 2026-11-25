// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// The narrow header parts exist so a site header keeps its row on a phone:
// the cycle theme button and the link to the other language. jsdom has no
// layout, so this spec measures the real one in a browser, with the real
// token classes (see harness/header.css).
//
// GovueHeader puts the navigation on a row of its own below 480px, its
// documented layout, and from 480px the header is one row of 56px with no
// wrapping at all. Issue 257 asked for no second row at 360px. The owner
// accepted two rows below 480px, the brand row and the navigation row, and
// one row from 480px. So the spec asks, per width:
//
// - Nothing sticks out of the viewport, and the page does not scroll sideways.
// - The brand, the theme button and the language link share one row.
// - From 480px the visible navigation control shares that row too. It is the
//   menu button below 640px and the call to action from 640px. The header is
//   exactly one row high.
// - Below 480px the header is the brand row plus the navigation row, and
//   nothing more.
import { expect, test, type Page } from '@playwright/test'

interface Box {
  name: string
  top: number
  bottom: number
  left: number
  right: number
}

/** measure returns the box of each visible part, in viewport pixels. */
async function measure(page: Page): Promise<{
  boxes: Box[]
  header: { height: number }
  row: { scrollWidth: number; clientWidth: number }
  doc: { scrollWidth: number; innerWidth: number }
}> {
  return page.evaluate(() => {
    const visible = (el: Element | null): el is HTMLElement =>
      el instanceof HTMLElement && el.getClientRects().length > 0
    const parts: Array<[string, Element | null]> = [
      ['brand', document.querySelector('[data-part="brand"]')],
      ['menu', document.querySelector('nav > button')],
      ['cta', document.querySelector('nav a[data-variant="cta"]')],
      ['theme', document.querySelector('button[data-mode]')],
      ['language', document.querySelector('[role="group"] a[hreflang]')],
    ]
    const boxes = parts
      .filter(([, el]) => visible(el))
      .map(([name, el]) => {
        const r = (el as HTMLElement).getBoundingClientRect()
        return { name, top: r.top, bottom: r.bottom, left: r.left, right: r.right }
      })
    const header = document.querySelector('header') as HTMLElement
    const row = header.firstElementChild as HTMLElement
    return {
      boxes,
      header: { height: header.getBoundingClientRect().height },
      row: { scrollWidth: row.scrollWidth, clientWidth: row.clientWidth },
      doc: {
        scrollWidth: document.documentElement.scrollWidth,
        innerWidth: window.innerWidth,
      },
    }
  })
}

const centre = (b: Box) => (b.top + b.bottom) / 2

/** HEADER_ROW is --spacing-header, 3.5rem. */
const HEADER_ROW = 56

for (const width of [360, 480, 640]) {
  test(`keeps the narrow header parts on one row at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 800 })
    await page.goto('/header.html')
    await expect(page.getByRole('banner')).toBeVisible()
    // The theme button and the language link are the parts under test.
    await expect(page.getByRole('button', { name: /^Theme: / })).toBeVisible()
    await expect(page.getByRole('link', { name: 'Deutsch' })).toBeVisible()

    const m = await measure(page)
    const byName = new Map(m.boxes.map((b) => [b.name, b]))
    /** box is the visible part of that name, and fails the test without one. */
    const box = (name: string): Box => {
      const b = byName.get(name)
      if (!b) throw new Error(`${name} is not visible at ${width}px`)
      return b
    }
    const brand = box('brand')
    const theme = box('theme')
    const language = box('language')

    // Nothing leaves the viewport, and nothing scrolls sideways.
    expect(m.doc.scrollWidth).toBeLessThanOrEqual(m.doc.innerWidth)
    expect(m.row.scrollWidth).toBeLessThanOrEqual(m.row.clientWidth)
    for (const b of m.boxes) {
      expect(b.left, b.name).toBeGreaterThanOrEqual(0)
      expect(b.right, b.name).toBeLessThanOrEqual(width)
    }

    // The theme button is the 44 by 44px target.
    expect(theme.bottom - theme.top).toBeCloseTo(44, 0)
    expect(theme.right - theme.left).toBeCloseTo(44, 0)

    if (width >= 480) {
      // The menu button below 640px, the call to action in the row from there.
      box(width >= 640 ? 'cta' : 'menu')
      expect(m.header.height).toBeLessThanOrEqual(HEADER_ROW + 1)
    } else {
      // The navigation row below the brand row, and no third row.
      expect(box('menu').top).toBeGreaterThanOrEqual(brand.bottom)
      expect(m.header.height).toBeLessThan(2 * HEADER_ROW + 10)
    }

    // One row: every part has the centre of the brand, within 2px of rounding.
    // A wrapped part sits a whole row, 44px or more, lower.
    const row = width >= 480 ? m.boxes : [brand, theme, language]
    for (const b of row) {
      expect(Math.abs(centre(b) - centre(brand)), `${b.name} at ${width}px`).toBeLessThanOrEqual(2)
    }
  })
}

// The open menu, issue 263. From 480px the header is one row of 56px, too low
// for the list. Between 480 and 639px the list leaves the flow and drops below
// the bar, over the page. Below 480px it is the navigation row, in the flow.
// Put the list back in the flow from 480px and it is centred on the 56px row:
// it sticks out above the window and covers the bar, and this spec fails.

interface MenuMeasure {
  viewport: { width: number; height: number }
  header: { top: number; bottom: number; height: number }
  toggle: { top: number; bottom: number; left: number; right: number }
  entries: Box[]
  list: { bottom: number; scrollHeight: number; clientHeight: number }
  content: { top: number }
}

/** measureMenu returns the header, the menu button, the list and its entries. */
async function measureMenu(page: Page): Promise<MenuMeasure> {
  return page.evaluate(() => {
    const rect = (el: Element) => el.getBoundingClientRect()
    const header = rect(document.querySelector('header') as HTMLElement)
    const toggle = rect(document.querySelector('nav > button') as HTMLElement)
    const list = document.querySelector('nav > ul') as HTMLElement
    const entries = Array.from(list.querySelectorAll('a')).map((a) => {
      const r = rect(a)
      return {
        name: a.textContent?.trim() ?? '',
        top: r.top,
        bottom: r.bottom,
        left: r.left,
        right: r.right,
      }
    })
    return {
      viewport: { width: window.innerWidth, height: window.innerHeight },
      header: { top: header.top, bottom: header.bottom, height: header.height },
      toggle: { top: toggle.top, bottom: toggle.bottom, left: toggle.left, right: toggle.right },
      entries,
      list: {
        bottom: rect(list).bottom,
        scrollHeight: list.scrollHeight,
        clientHeight: list.clientHeight,
      },
      content: { top: rect(document.querySelector('main') as HTMLElement).top },
    }
  })
}

for (const width of [360, 480, 560, 639]) {
  test(`drops the open menu below the bar at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 800 })
    await page.goto('/header.html')
    const toggle = page.getByRole('button', { name: 'Menu' })
    await expect(toggle).toBeVisible()
    const closed = await measureMenu(page)

    await toggle.click()
    await expect(toggle).toHaveAttribute('aria-expanded', 'true')
    await expect(page.getByRole('link', { name: 'Projects' })).toBeVisible()
    const m = await measureMenu(page)

    // The menu button stays on screen, in the bar, and is not pushed out.
    await expect(toggle).toBeVisible()
    expect(m.toggle.top).toBeGreaterThanOrEqual(m.header.top)
    expect(m.toggle.bottom).toBeLessThanOrEqual(m.header.bottom)
    expect(m.toggle.left).toBeGreaterThanOrEqual(0)
    expect(m.toggle.right).toBeLessThanOrEqual(width)

    // Every entry is inside the window and below the menu button.
    expect(m.entries.length).toBeGreaterThan(0)
    for (const e of m.entries) {
      expect(e.top, `${e.name} top at ${width}px`).toBeGreaterThanOrEqual(0)
      expect(e.bottom, `${e.name} bottom at ${width}px`).toBeLessThanOrEqual(m.viewport.height)
      expect(e.left, `${e.name} left at ${width}px`).toBeGreaterThanOrEqual(0)
      expect(e.right, `${e.name} right at ${width}px`).toBeLessThanOrEqual(width)
      expect(e.top, `${e.name} under the button at ${width}px`).toBeGreaterThanOrEqual(
        m.toggle.bottom,
      )
    }
    const first = m.entries[0]

    if (width >= 480) {
      // Out of the flow: the header keeps its one row and the page does not move.
      expect(m.header.height).toBeLessThanOrEqual(HEADER_ROW + 1)
      expect(m.header.height).toBeCloseTo(closed.header.height, 0)
      expect(m.content.top).toBeCloseTo(closed.content.top, 0)
      // The list starts at the bottom of the bar.
      expect(first.top, `first entry at ${width}px`).toBeGreaterThanOrEqual(m.header.bottom)
    } else {
      // In the flow: the list is the navigation row, inside the header.
      expect(m.list.bottom).toBeLessThanOrEqual(m.header.bottom + 1)
    }

    // Escape closes the menu and returns focus to the button.
    await page.getByRole('link', { name: 'Projects' }).focus()
    await page.keyboard.press('Escape')
    await expect(toggle).toHaveAttribute('aria-expanded', 'false')
    await expect(toggle).toBeFocused()
  })
}

test('scrolls the dropped menu when it is taller than the window below the bar', async ({
  page,
}) => {
  // 140px: the bar takes 57, the list of two 44px entries needs about 108.
  await page.setViewportSize({ width: 560, height: 140 })
  await page.goto('/header.html')
  await page.getByRole('button', { name: 'Menu' }).click()

  const m = await measureMenu(page)
  expect(m.list.bottom).toBeLessThanOrEqual(m.viewport.height + 1)
  expect(m.list.scrollHeight).toBeGreaterThan(m.list.clientHeight)

  // The last entry is reachable: scrolled into view it lies inside the window.
  const last = page.getByRole('link', { name: 'Contact' })
  await last.scrollIntoViewIfNeeded()
  const box = await last.boundingBox()
  expect(box).not.toBeNull()
  expect((box?.y ?? 0) + (box?.height ?? 0)).toBeLessThanOrEqual(140 + 1)
  expect(box?.y ?? -1).toBeGreaterThanOrEqual(0)
})
