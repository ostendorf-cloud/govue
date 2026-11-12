// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// GovueDialog.test.ts already covers a synthetic userEvent.keyboard Escape
// press. What it cannot verify is that a real, OS-dispatched Escape key
// press - through the browser's real input pipeline, not jsdom's simulated
// one - behaves the same way: it dismisses a dismissible dialog and leaves a
// non-dismissible one open.
import { expect, test } from '@playwright/test'

test.describe('a real Escape key press', () => {
  test('dismisses a dismissible GovueDialog', async ({ page }) => {
    await page.goto('/')
    await page.getByRole('button', { name: 'Open dialog' }).click()
    await expect(page.getByRole('dialog')).toBeVisible()

    await page.keyboard.press('Escape')

    await expect(page.getByRole('dialog')).toBeHidden()
  })

  test('leaves a non-dismissible GovueDialog open', async ({ page }) => {
    await page.goto('/?dismissible=false')
    await page.getByRole('button', { name: 'Open dialog' }).click()
    await expect(page.getByRole('dialog')).toBeVisible()

    await page.keyboard.press('Escape')

    await expect(page.getByRole('dialog')).toBeVisible()
  })

  test('leaves GovueConfirmDialog open, since it is never dismissible', async ({ page }) => {
    await page.goto('/')
    await page.getByRole('button', { name: 'Open confirm dialog' }).click()
    await expect(page.getByRole('dialog')).toBeVisible()

    await page.keyboard.press('Escape')

    await expect(page.getByRole('dialog')).toBeVisible()
  })
})
