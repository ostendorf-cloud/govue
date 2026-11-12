// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import userEvent from '@testing-library/user-event'
import { render, screen } from '@testing-library/vue'
import { describe, expect, it, vi } from 'vitest'
import { h } from 'vue'

import GovueEmptyState from './GovueEmptyState.vue'

const props = {
  title: 'No deployments yet',
  description: 'A deployment is created whenever a build is promoted. Trigger the first one.',
}

describe('GovueEmptyState', () => {
  it('says what this is and why it is empty', () => {
    render(GovueEmptyState, { props })

    expect(screen.getByRole('heading', { name: 'No deployments yet' })).toBeVisible()
    expect(screen.getByText(props.description)).toBeVisible()
  })

  it('offers the action that fills it', async () => {
    const onClick = vi.fn()
    render(GovueEmptyState, {
      props,
      slots: {
        action: () => h('button', { type: 'button', onClick }, 'Trigger deployment'),
      },
    })

    await userEvent.click(screen.getByRole('button', { name: 'Trigger deployment' }))

    expect(onClick).toHaveBeenCalledTimes(1)
  })

  it('keeps a decorative icon out of the accessibility tree', () => {
    render(GovueEmptyState, {
      props,
      slots: { icon: () => h('svg', { 'aria-hidden': 'true', 'data-testid': 'icon' }) },
    })

    // The heading and the description carry the meaning, so the icon adds
    // nothing a screen reader has to hear.
    expect(screen.getByRole('heading', { name: 'No deployments yet' })).toBeVisible()
    expect(screen.queryByRole('img')).toBeNull()
  })

  it('works without an icon and without an action', () => {
    render(GovueEmptyState, { props })

    expect(screen.queryByRole('button')).toBeNull()
    expect(screen.getByRole('heading', { name: 'No deployments yet' })).toBeVisible()
  })
})
