// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { fireEvent, render, screen } from '@testing-library/vue'
import { describe, expect, it } from 'vitest'

import WkSwitch from './WkSwitch.vue'

describe('WkSwitch', () => {
  it('is a switch with its label as the accessible name', () => {
    render(WkSwitch, { props: { label: 'Dark mode' } })

    const control = screen.getByRole('switch', { name: 'Dark mode' })
    expect(control).toBeVisible()
    expect(control).not.toBeChecked()
  })

  it('writes the new state back into the model on activation', async () => {
    const view = render(WkSwitch, { props: { label: 'Dark mode' } })

    await fireEvent.click(screen.getByRole('switch', { name: 'Dark mode' }))

    expect(view.emitted('update:modelValue').at(-1)).toEqual([true])
    expect(screen.getByRole('switch', { name: 'Dark mode' })).toBeChecked()
  })

  it('shows the state it was given', () => {
    render(WkSwitch, { props: { label: 'Dark mode', modelValue: true } })

    expect(screen.getByRole('switch', { name: 'Dark mode' })).toHaveAttribute(
      'aria-checked',
      'true',
    )
  })

  it('toggles back on a second activation', async () => {
    render(WkSwitch, { props: { label: 'Dark mode' } })

    const control = screen.getByRole('switch', { name: 'Dark mode' })
    await fireEvent.click(control)
    expect(control).toBeChecked()

    await fireEvent.click(control)
    expect(control).not.toBeChecked()
  })

  it('is a disabled native button, which the browser alone keeps from acting', () => {
    render(WkSwitch, { props: { label: 'Dark mode', disabled: true } })

    // A real click never reaches a disabled button's listeners; that is the
    // platform's own guarantee, not something to re-implement or re-test
    // here, only the attribute that switches it on.
    expect(screen.getByRole('switch', { name: 'Dark mode' })).toBeDisabled()
  })

  it('is a touch sized control', () => {
    render(WkSwitch, { props: { label: 'Dark mode' } })

    expect(screen.getByRole('switch', { name: 'Dark mode' }).className).toContain('min-h-11')
  })
})
