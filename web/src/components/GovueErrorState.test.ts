// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import userEvent from '@testing-library/user-event'
import { render, screen } from '@testing-library/vue'
import { describe, expect, it } from 'vitest'

import GovueErrorState from './GovueErrorState.vue'

const props = {
  title: 'The deployment did not load',
  message: 'The server did not answer. Try again, or come back in a minute.',
}

describe('GovueErrorState', () => {
  it('announces itself, because nobody asked for it', () => {
    render(GovueErrorState, { props })

    expect(screen.getByRole('alert')).toHaveTextContent('The deployment did not load')
  })

  it('says what failed and what to do next', () => {
    render(GovueErrorState, { props })

    expect(screen.getByRole('heading', { name: 'The deployment did not load' })).toBeVisible()
    expect(screen.getByText(props.message)).toBeVisible()
  })

  it('renders the title as a second level heading by default', () => {
    render(GovueErrorState, { props })

    expect(
      screen.getByRole('heading', { level: 2, name: 'The deployment did not load' }),
    ).toBeVisible()
  })

  it.each([3, 4, 5, 6] as const)('renders the title at level %i when asked to', (level) => {
    render(GovueErrorState, { props: { ...props, headingLevel: level } })

    expect(
      screen.getByRole('heading', { level, name: 'The deployment did not load' }),
    ).toBeVisible()
  })

  it.each([1, 7, 0, -2, Number.NaN, 2.5, '3', null])(
    'falls back to a second level heading for %j, which is not a heading level',
    (level) => {
      render(GovueErrorState, { props: { ...props, headingLevel: level as never } })

      expect(
        screen.getByRole('heading', { level: 2, name: 'The deployment did not load' }),
      ).toBeVisible()
    },
  )

  it('offers a retry and reports it', async () => {
    const view = render(GovueErrorState, { props })

    await userEvent.click(screen.getByRole('button', { name: 'Try again' }))

    expect(view.emitted()).toHaveProperty('retry')
    expect(view.emitted('retry')).toHaveLength(1)
  })

  it('retries from the keyboard', async () => {
    const view = render(GovueErrorState, { props })

    await userEvent.tab()
    expect(screen.getByRole('button', { name: 'Try again' })).toHaveFocus()
    await userEvent.keyboard('{Enter}')

    expect(view.emitted('retry')).toHaveLength(1)
  })

  it('shows the request id when the server gave one', () => {
    render(GovueErrorState, { props: { ...props, requestId: '01JF3Z4K' } })

    expect(screen.getByText('01JF3Z4K')).toBeVisible()
  })

  it('leaves the request id out when there is none', () => {
    render(GovueErrorState, { props })

    expect(screen.queryByText(/Request id/)).toBeNull()
  })

  it('does not retry twice while a retry is in flight', async () => {
    const view = render(GovueErrorState, { props: { ...props, loading: true } })

    const button = screen.getByRole('button', { name: /Try again/ })
    expect(button).toBeDisabled()
    await userEvent.click(button)

    expect(view.emitted()).not.toHaveProperty('retry')
  })

  it('takes its own wording for the retry', () => {
    render(GovueErrorState, { props: { ...props, retryLabel: 'Reload' } })

    expect(screen.getByRole('button', { name: 'Reload' })).toBeVisible()
  })
})
