// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { fireEvent, render, screen } from '@testing-library/vue'
import { describe, expect, it } from 'vitest'
import { defineComponent, h } from 'vue'

import GovueCheckbox from './GovueCheckbox.vue'

describe('GovueCheckbox', () => {
  it('labels itself with its own text', () => {
    render(GovueCheckbox, { props: { label: 'Send me the weekly mail' } })

    expect(screen.getByRole('checkbox', { name: 'Send me the weekly mail' })).toBeVisible()
  })

  it('writes the new state back into the model on a click', async () => {
    const view = render(GovueCheckbox, { props: { label: 'Send me the weekly mail' } })

    await fireEvent.click(screen.getByRole('checkbox', { name: 'Send me the weekly mail' }))

    expect(view.emitted('update:modelValue').at(-1)).toEqual([true])
  })

  it('toggles from a click on the label text as well, since the row is the label', async () => {
    render(GovueCheckbox, { props: { label: 'Send me the weekly mail' } })

    await fireEvent.click(screen.getByText('Send me the weekly mail'))

    expect(screen.getByRole('checkbox', { name: 'Send me the weekly mail' })).toBeChecked()
  })

  it('shows the state it was given', () => {
    render(GovueCheckbox, { props: { label: 'Send me the weekly mail', modelValue: true } })

    expect(screen.getByRole('checkbox', { name: 'Send me the weekly mail' })).toBeChecked()
  })

  it('describes the choice with its hint', () => {
    render(GovueCheckbox, {
      props: { label: 'Send me the weekly mail', hint: 'One mail every Monday' },
    })

    expect(
      screen.getByRole('checkbox', { name: 'Send me the weekly mail' }),
    ).toHaveAccessibleDescription('One mail every Monday')
  })

  it('is a disabled native checkbox, which the browser alone keeps from toggling', () => {
    render(GovueCheckbox, { props: { label: 'Send me the weekly mail', disabled: true } })

    // A real click never reaches a disabled checkbox's toggling behaviour;
    // that is the platform's own guarantee, not something to re-implement or
    // re-test here, only the attribute that switches it on.
    expect(screen.getByRole('checkbox', { name: 'Send me the weekly mail' })).toBeDisabled()
  })

  it('keeps the aria-describedby a caller passes and adds the hint to it', () => {
    render(
      defineComponent({
        setup: () => () =>
          h('div', [
            h('p', { id: 'err' }, 'Please agree.'),
            h(GovueCheckbox, { label: 'Agree', hint: 'Required', 'aria-describedby': 'err' }),
          ]),
      }),
    )

    expect(screen.getByRole('checkbox', { name: 'Agree' })).toHaveAccessibleDescription(
      'Please agree. Required',
    )
  })

  it('keeps the aria-describedby a caller passes when there is no hint', () => {
    render(
      defineComponent({
        setup: () => () =>
          h('div', [
            h('p', { id: 'err' }, 'Please agree.'),
            h(GovueCheckbox, { label: 'Agree', 'aria-describedby': 'err' }),
          ]),
      }),
    )

    expect(screen.getByRole('checkbox', { name: 'Agree' })).toHaveAccessibleDescription(
      'Please agree.',
    )
  })

  it('has a hit area a finger reaches', () => {
    render(GovueCheckbox, { props: { label: 'Send me the weekly mail' } })

    const row = screen.getByRole('checkbox', { name: 'Send me the weekly mail' }).closest('label')
    expect(row?.className).toContain('min-h-11')
  })
})
