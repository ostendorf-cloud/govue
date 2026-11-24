// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// The narrow header parts exist so a site header keeps its row on a phone:
// the cycle theme button and the link to the other language. jsdom has no
// layout, so this spec measures the real one in a browser, with the real
// token classes (see harness/header.css).
//
// GovueHeader puts the navigation on a row of its own below 480px, its
// documented layout, and from 480px the header is one row of 56px with no
// wrapping at all. So the spec asks, per width:
//
// - nothing sticks out of the viewport and the page does not scroll sideways;
// - the brand, the theme button and the language link share one row;
// - from 480px the visible navigation control (the menu button below 640px,
//   the call to action from 640px) shares that row too, and the header is
//   exactly one row high;
// - below 480px the header is the brand row plus the navigation row, nothing
//   more.
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
