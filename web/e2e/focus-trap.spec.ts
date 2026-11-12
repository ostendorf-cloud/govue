// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// jsdom has no layout engine, so it cannot tell an off-screen or hidden
// focusable element apart from a visible one: GovueDialog.test.ts documents why
// the focus trap itself needs a real browser. This suite presses Tab for
// real, through Playwright's keyboard, all the way to the last focusable
// element inside an open dialog and checks that the next Tab press wraps
// focus back to the first one inside it, never to anything behind it.
import { expect, test } from '@playwright/test'

test.describe('focus trap', () => {
  test('wraps from the last to the first focusable element inside GovueDialog', async ({
    page,
  }) => {
    await page.goto('/')
    await page.getByRole('button', { name: 'Open dialog' }).click()

    const link = page.locator('#dialog-link')
    const action = page.locator('#dialog-action')

    // Reka focuses the first focusable element in the content as soon as
    // the dialog opens.
    await expect(link).toBeFocused()

    await page.keyboard.press('Tab')
    await expect(action).toBeFocused()

    await page.keyboard.press('Tab')
    await expect(link).toBeFocused()
  })

  test('wraps from Delete back to Cancel inside GovueConfirmDialog', async ({ page }) => {
    await page.goto('/')
    await page.getByRole('button', { name: 'Open confirm dialog' }).click()

    const cancel = page.getByRole('button', { name: 'Cancel' })
    const confirmButton = page.getByRole('button', { name: 'Delete' })

    await expect(cancel).toBeFocused()

    await page.keyboard.press('Tab')
    await expect(confirmButton).toBeFocused()

    await page.keyboard.press('Tab')
    await expect(cancel).toBeFocused()
  })
})
