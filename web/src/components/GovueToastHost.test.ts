// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import userEvent from '@testing-library/user-event'
import { render, screen, within } from '@testing-library/vue'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { nextTick } from 'vue'

import { useToasts } from '../composables/useToasts'
import GovueToastHost from './GovueToastHost.vue'

const { toasts, notify, dismissAll } = useToasts()

describe('GovueToastHost', () => {
  beforeEach(dismissAll)
  afterEach(dismissAll)

  it('shows the title and the message of a toast', async () => {
    render(GovueToastHost)

    notify({ kind: 'success', title: 'Changes saved', message: 'Two files changed' })
    await nextTick()

    expect(screen.getByText('Changes saved')).toBeVisible()
    expect(screen.getByText('Two files changed')).toBeVisible()
  })

  it('waits for a pause with everything that is not a failure', async () => {
    render(GovueToastHost)

    notify({ kind: 'info', title: 'Sync running' })
    await nextTick()

    const polite = screen.getByRole('status')
    expect(polite).toHaveAttribute('aria-live', 'polite')
    expect(within(polite).getByText('Sync running')).toBeVisible()
  })

  it('interrupts with a failure', async () => {
    render(GovueToastHost)

    notify({ kind: 'error', title: 'Saving failed', message: 'Request req-12' })
    await nextTick()

    const urgent = screen.getByRole('alert')
    expect(within(urgent).getByText('Saving failed')).toBeVisible()
    expect(within(urgent).getByText('Error')).toBeVisible()
  })

  it('keeps both regions in the page while there is nothing to say', () => {
    render(GovueToastHost)

    expect(screen.getByRole('status')).toBeInTheDocument()
    expect(screen.getByRole('alert')).toBeInTheDocument()
    expect(screen.queryByRole('list')).toBeNull()
  })

  it('drops the toast the user dismisses', async () => {
    render(GovueToastHost)

    notify({ kind: 'error', title: 'Saving failed' })
    await nextTick()

    await userEvent.click(screen.getByRole('button', { name: 'Dismiss' }))

    expect(screen.queryByText('Saving failed')).toBeNull()
    expect(toasts.value).toHaveLength(0)
  })

  it('dismisses only the toast that was asked for', async () => {
    render(GovueToastHost)

    notify({ kind: 'error', title: 'First failure' })
    notify({ kind: 'error', title: 'Second failure' })
    await nextTick()

    const items = screen.getAllByRole('listitem')
    await userEvent.click(within(items[0]).getByRole('button', { name: 'Dismiss' }))

    expect(screen.queryByText('First failure')).toBeNull()
    expect(screen.getByText('Second failure')).toBeVisible()
  })

  it('lists the toasts of one region as a list', async () => {
    render(GovueToastHost)

    notify({ kind: 'error', title: 'First failure' })
    notify({ kind: 'error', title: 'Second failure' })
    await nextTick()

    expect(within(screen.getByRole('list')).getAllByRole('listitem')).toHaveLength(2)
  })
})
