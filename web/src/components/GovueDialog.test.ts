// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// What this suite cannot verify, even with Reka UI doing the real work under
// the hood:
//
// - The focus trap. jsdom has no layout engine, so nothing here is actually
//   visible or positioned; a check that Tab-cycling stays inside the dialog
//   can only ever exercise the same DOM nodes a sighted check would consider
//   tabbable, never confirm that an off-screen or hidden focusable element
//   was correctly skipped, because jsdom cannot tell "off-screen" from
//   "on-screen" in the first place.
// - The inert background. Reka marks every sibling outside the dialog
//   `aria-hidden="true"` (through the `aria-hidden` package's `hideOthers`,
//   not the `inert` attribute - this build does not use `inert`). jsdom does
//   not enforce what `aria-hidden` means any more than it enforces `inert`:
//   a synthetic click or `.focus()` call still reaches a node marked
//   `aria-hidden="true"` in a test. So a passing assertion here can only
//   confirm the marker was applied, never that a background interaction was
//   actually blocked.
import userEvent from '@testing-library/user-event'
import { render, screen } from '@testing-library/vue'
import { describe, expect, it } from 'vitest'
import { h, nextTick } from 'vue'

import GovueDialog from './GovueDialog.vue'

// Reka's portal only teleports its content once the component has actually
// mounted (it gates on a `isMounted` ref flipped in onMounted, which Vue
// commits in the next reactive flush rather than inside the synchronous
// render call), so every test that expects the dialog to be in the DOM
// awaits one tick after rendering it open.
async function open(props: Record<string, unknown> = {}) {
  const view = render(GovueDialog, {
    props: { title: 'Discard the draft?', open: true, ...props },
    slots: {
      default: () => 'Any changes since the last save are lost.',
      actions: () => h('button', { type: 'button' }, 'Discard'),
    },
  })
  await nextTick()
  return view
}

/** The backdrop. It has no accessible role - Reka marks it aria-hidden along
 *  with every other sibling of the dialog content - so it is reached by its
 *  own token class instead. */
function backdrop(): HTMLElement {
  const element = document.querySelector<HTMLElement>('.bg-overlay')
  if (!element) {
    throw new Error('backdrop not found')
  }
  return element
}

describe('GovueDialog', () => {
  it('is a dialog named by its heading', async () => {
    await open({ description: 'This cannot be undone.' })

    const dialog = screen.getByRole('dialog')
    expect(dialog).toHaveAccessibleName('Discard the draft?')
    expect(dialog).toHaveAccessibleDescription('This cannot be undone.')
    expect(screen.getByRole('heading', { name: 'Discard the draft?' })).toBeVisible()
  })

  it('shows the content and the actions', async () => {
    await open()

    expect(screen.getByText('Any changes since the last save are lost.')).toBeVisible()
    expect(screen.getByRole('button', { name: 'Discard' })).toBeVisible()
  })

  it('is not in the page while closed', () => {
    render(GovueDialog, { props: { title: 'Discard the draft?' } })

    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('closes on Escape and reports it', async () => {
    const { emitted } = await open()

    await userEvent.keyboard('{Escape}')

    expect(emitted('update:open').at(-1)).toEqual([false])
    expect(emitted()).toHaveProperty('close')
  })

  it('stays open on Escape when it must be answered', async () => {
    const { emitted } = await open({ dismissible: false })

    await userEvent.keyboard('{Escape}')

    expect(emitted()).not.toHaveProperty('update:open')
    expect(emitted()).not.toHaveProperty('close')
    expect(screen.getByRole('dialog')).toBeVisible()
  })

  it('closes on a pointer interaction on the backdrop and reports it', async () => {
    const { emitted } = await open()

    await userEvent.click(backdrop())

    expect(emitted('update:open').at(-1)).toEqual([false])
    expect(emitted()).toHaveProperty('close')
  })

  it('keeps a click inside it to itself', async () => {
    const { emitted } = await open()

    await userEvent.click(screen.getByText('Any changes since the last save are lost.'))

    expect(emitted()).not.toHaveProperty('update:open')
  })

  it('does not close on a backdrop click when it must be answered', async () => {
    const { emitted } = await open({ dismissible: false })

    await userEvent.click(backdrop())

    expect(emitted()).not.toHaveProperty('update:open')
  })

  it('uses the requested size', async () => {
    await open({ size: 'lg' })

    expect(screen.getByRole('dialog').className).toContain('max-w-2xl')
  })
})
