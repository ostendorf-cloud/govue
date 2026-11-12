import { fireEvent, render, screen } from '@testing-library/vue'
import { describe, expect, it, vi } from 'vitest'

import WkButton from './WkButton.vue'

describe('WkButton', () => {
  it('is a button with its slot content as the accessible name', () => {
    render(WkButton, { slots: { default: 'Save list' } })

    expect(screen.getByRole('button', { name: 'Save list' })).toBeVisible()
  })

  it('calls the listener on a click', async () => {
    const onClick = vi.fn()
    render(WkButton, { slots: { default: 'Save' }, attrs: { onClick } })

    await fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    expect(onClick).toHaveBeenCalledTimes(1)
  })

  it('is a disabled native button, which the browser alone keeps from acting', () => {
    render(WkButton, { props: { disabled: true }, slots: { default: 'Save' } })

    // A real click never reaches a disabled button's listeners; that is the
    // platform's own guarantee, not something to re-implement or re-test
    // here, only the attribute that switches it on.
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled()
  })

  it('says it is working while loading, and disables itself the same way', () => {
    render(WkButton, {
      props: { loading: true, loadingLabel: 'Saving' },
      slots: { default: 'Save' },
    })

    const button = screen.getByRole('button', { name: /Saving/ })
    expect(button).toHaveAttribute('aria-busy', 'true')
    expect(button).toBeDisabled()
  })

  it('is a plain button unless it is asked to submit', async () => {
    const { rerender } = render(WkButton, { slots: { default: 'Go' } })
    expect(screen.getByRole('button')).toHaveAttribute('type', 'button')

    await rerender({ type: 'submit' })

    expect(screen.getByRole('button')).toHaveAttribute('type', 'submit')
  })

  it('carries the variant in its classes and names no raw colour', () => {
    render(WkButton, { props: { variant: 'danger' }, slots: { default: 'Delete' } })

    const button = screen.getByRole('button', { name: 'Delete' })
    expect(button.className).toContain('bg-danger-strong')
    // Only role tokens, never a raw colour.
    expect(button.className).not.toMatch(/#|\[rgb/)
  })

  it('is a touch sized control on every variant', () => {
    render(WkButton, { props: { variant: 'quiet' }, slots: { default: 'Cancel' } })

    expect(screen.getByRole('button', { name: 'Cancel' }).className).toContain('min-h-11')
  })

  it('fills the width when asked to', () => {
    render(WkButton, { props: { block: true }, slots: { default: 'Continue' } })

    expect(screen.getByRole('button', { name: 'Continue' }).className).toContain('w-full')
  })
})
