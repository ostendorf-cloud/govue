// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import userEvent from '@testing-library/user-event'
import { render, screen } from '@testing-library/vue'
import { describe, expect, it } from 'vitest'
import { nextTick } from 'vue'

import WkConfirmDialog from './WkConfirmDialog.vue'

const props = {
  title: 'Discard the draft?',
  message: 'Any changes since the last save are lost. This cannot be undone.',
  subject: 'Release notes',
  open: true,
}

// See WkDialog.test.ts: the dialog this wraps only reaches the DOM one tick
// after render, because Reka's portal gates on a ref flipped in onMounted.
async function openDialog(overrides: Record<string, unknown> = {}) {
  const view = render(WkConfirmDialog, { props: { ...props, ...overrides } })
  await nextTick()
  return view
}

describe('WkConfirmDialog', () => {
  it('is a dialog named by its question and described by what happens', async () => {
    await openDialog()

    const dialog = screen.getByRole('dialog')
    expect(dialog).toHaveAccessibleName('Discard the draft?')
    expect(dialog).toHaveAccessibleDescription(props.message)
  })

  it('names the thing it is about to discard', async () => {
    await openDialog()

    expect(screen.getByText('Release notes')).toBeVisible()
  })

  it('offers Delete and Cancel by default', async () => {
    await openDialog()

    expect(screen.getByRole('button', { name: 'Delete' })).toBeVisible()
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeVisible()
  })

  it('takes its own wording for both buttons', async () => {
    await openDialog({ confirmLabel: 'Discard draft', cancelLabel: 'Keep editing' })

    expect(screen.getByRole('button', { name: 'Discard draft' })).toBeVisible()
    expect(screen.getByRole('button', { name: 'Keep editing' })).toBeVisible()
  })

  // Reka's focus scope moves focus into the dialog as soon as it opens,
  // rather than leaving it for the first Tab press the way the native
  // <dialog> element this component used to be built on did.
  it('puts the way out within reach of the keyboard first', async () => {
    await openDialog()

    expect(screen.getByRole('button', { name: 'Cancel' })).toHaveFocus()
  })

  it('reports the confirmation and leaves the closing to the caller', async () => {
    const view = await openDialog()

    await userEvent.click(screen.getByRole('button', { name: 'Delete' }))

    expect(view.emitted('confirm')).toHaveLength(1)
    expect(view.emitted()).not.toHaveProperty('update:open')
  })

  it('closes on Cancel without confirming', async () => {
    const view = await openDialog()

    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))

    expect(view.emitted('update:open').at(-1)).toEqual([false])
    expect(view.emitted()).not.toHaveProperty('confirm')
  })

  it('does not close on Escape, because the question has to be answered', async () => {
    const view = await openDialog()

    await userEvent.keyboard('{Escape}')

    expect(view.emitted()).not.toHaveProperty('update:open')
    expect(screen.getByRole('dialog')).toBeVisible()
  })

  it('does not close on a backdrop click', async () => {
    const view = await openDialog()

    const backdrop = document.querySelector<HTMLElement>('.bg-overlay')
    if (!backdrop) {
      throw new Error('backdrop not found')
    }
    await userEvent.click(backdrop)

    expect(view.emitted()).not.toHaveProperty('update:open')
  })

  it('takes neither answer twice while the action runs', async () => {
    const view = await openDialog({ loading: true })

    const confirmButton = screen.getByRole('button', { name: /Delete/ })
    expect(confirmButton).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled()
    await userEvent.click(confirmButton)

    expect(view.emitted()).not.toHaveProperty('confirm')
  })

  it('is not in the page while closed', async () => {
    await openDialog({ open: false })

    expect(screen.queryByRole('dialog')).toBeNull()
  })
})
