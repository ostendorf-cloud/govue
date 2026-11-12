// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Reka marks every sibling outside the dialog aria-hidden, through the
// aria-hidden package's hideOthers, not the inert attribute. jsdom does not
// enforce what aria-hidden means: a synthetic click or .focus() call still
// reaches a node marked aria-hidden="true" in a test, so WkDialog.test.ts
// can only confirm the marker was applied, never that interaction is
// actually blocked. This suite drives a real pointer click and a real Tab
// sequence and checks that neither one ever reaches the page content behind
// an open dialog.
import { expect, test, type Page } from '@playwright/test'

// A plain locator click() refuses to fire when something else is on top of
// the target, which is exactly the case this suite wants to force: a click
// at the button's own on-screen position, left for the browser to resolve
// against whatever actually sits there.
async function clickAt(page: Page, selector: string): Promise<void> {
  const box = await page.locator(selector).boundingBox()
  if (!box) {
    throw new Error(`${selector} has no bounding box`)
  }
  await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2)
}

test.describe('inert background', () => {
  test('a pointer click behind an open WkDialog never reaches the page buttons', async ({
    page,
  }) => {
    // Non-dismissible, so the first click at the backdrop's position cannot
    // close the dialog on its own and leave the second click facing an
    // already-closed one: this test is about where the click lands, not
    // about dismissal, which escape-dismiss.spec.ts already covers.
    await page.goto('/?dismissible=false')
    await page.getByRole('button', { name: 'Open dialog' }).click()
    await expect(page.getByRole('dialog')).toBeVisible()

    await clickAt(page, '#page-before')
    await clickAt(page, '#page-after')

    await expect(page.locator('#page-before-count')).toHaveText('0')
    await expect(page.locator('#page-after-count')).toHaveText('0')
  })

  test('Tab never reaches the page buttons while WkDialog is open', async ({ page }) => {
    await page.goto('/')
    await page.getByRole('button', { name: 'Open dialog' }).click()
    await expect(page.getByRole('dialog')).toBeVisible()

    for (let i = 0; i < 10; i += 1) {
      await page.keyboard.press('Tab')
      const activeId = await page.evaluate(() => document.activeElement?.id ?? '')
      expect(activeId).not.toBe('page-before')
      expect(activeId).not.toBe('page-after')
    }
  })

  test('a pointer click behind an open WkConfirmDialog never reaches the page buttons', async ({
    page,
  }) => {
    await page.goto('/')
    await page.getByRole('button', { name: 'Open confirm dialog' }).click()
    await expect(page.getByRole('dialog')).toBeVisible()

    await clickAt(page, '#page-before')
    await clickAt(page, '#page-after')

    await expect(page.locator('#page-before-count')).toHaveText('0')
    await expect(page.locator('#page-after-count')).toHaveText('0')
  })
})
