import userEvent from '@testing-library/user-event'
import { render, screen } from '@testing-library/vue'
import { describe, expect, it } from 'vitest'

import WkErrorState from './WkErrorState.vue'

const props = {
  title: 'The deployment did not load',
  message: 'The server did not answer. Try again, or come back in a minute.',
}

describe('WkErrorState', () => {
  it('announces itself, because nobody asked for it', () => {
    render(WkErrorState, { props })

    expect(screen.getByRole('alert')).toHaveTextContent('The deployment did not load')
  })

  it('says what failed and what to do next', () => {
    render(WkErrorState, { props })

    expect(screen.getByRole('heading', { name: 'The deployment did not load' })).toBeVisible()
    expect(screen.getByText(props.message)).toBeVisible()
  })

  it('offers a retry and reports it', async () => {
    const view = render(WkErrorState, { props })

    await userEvent.click(screen.getByRole('button', { name: 'Try again' }))

    expect(view.emitted()).toHaveProperty('retry')
    expect(view.emitted('retry')).toHaveLength(1)
  })

  it('retries from the keyboard', async () => {
    const view = render(WkErrorState, { props })

    await userEvent.tab()
    expect(screen.getByRole('button', { name: 'Try again' })).toHaveFocus()
    await userEvent.keyboard('{Enter}')

    expect(view.emitted('retry')).toHaveLength(1)
  })

  it('shows the request id when the server gave one', () => {
    render(WkErrorState, { props: { ...props, requestId: '01JF3Z4K' } })

    expect(screen.getByText('01JF3Z4K')).toBeVisible()
  })

  it('leaves the request id out when there is none', () => {
    render(WkErrorState, { props })

    expect(screen.queryByText(/Request id/)).toBeNull()
  })

  it('does not retry twice while a retry is in flight', async () => {
    const view = render(WkErrorState, { props: { ...props, loading: true } })

    const button = screen.getByRole('button', { name: /Try again/ })
    expect(button).toBeDisabled()
    await userEvent.click(button)

    expect(view.emitted()).not.toHaveProperty('retry')
  })

  it('takes its own wording for the retry', () => {
    render(WkErrorState, { props: { ...props, retryLabel: 'Reload' } })

    expect(screen.getByRole('button', { name: 'Reload' })).toBeVisible()
  })
})
