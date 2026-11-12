// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { fireEvent, render, screen, waitFor } from '@testing-library/vue'
import { describe, expect, it } from 'vitest'
import { defineComponent, h } from 'vue'

import GovueField from './GovueField.vue'
import GovueSelect from './GovueSelect.vue'

type SelectProps = InstanceType<typeof GovueSelect>['$props']

const options = [
  { value: 'red', label: 'Red' },
  { value: 'blue', label: 'Blue' },
  { value: 'gold', label: 'Gold', disabled: true },
]

/**
 * standalone renders the control on its own. The name then comes from an
 * attribute, which is why it is passed as one; inside a field the label
 * supplies it.
 */
function standalone(props: Partial<SelectProps> = {}) {
  return render(GovueSelect, { props: { options, ...props }, attrs: { 'aria-label': 'Colour' } })
}

describe('GovueSelect', () => {
  it('is a combobox with its options collapsed until it is opened', async () => {
    standalone()

    const trigger = screen.getByRole('combobox', { name: 'Colour' })
    expect(trigger).toBeVisible()
    expect(screen.queryByRole('option')).toBeNull()

    await fireEvent.keyDown(trigger, { key: 'ArrowDown' })

    expect(screen.getAllByRole('option').map((option) => option.textContent?.trim())).toEqual([
      'Red',
      'Blue',
      'Gold',
    ])
  })

  it('shows a placeholder until something is chosen', () => {
    standalone({ placeholder: 'Pick a colour' })

    expect(screen.getByRole('combobox', { name: 'Colour' })).toHaveTextContent('Pick a colour')
  })

  it('shows the label of the value it was given', () => {
    standalone({ modelValue: 'red' })

    expect(screen.getByRole('combobox', { name: 'Colour' })).toHaveTextContent('Red')
  })

  it('writes the chosen value back into the model', async () => {
    const view = standalone()
    const trigger = screen.getByRole('combobox', { name: 'Colour' })

    await fireEvent.keyDown(trigger, { key: 'ArrowDown' })
    const blue = screen.getByRole('option', { name: 'Blue' })
    blue.focus()
    await fireEvent.keyDown(blue, { key: 'Enter' })

    // Selecting an item goes through a chain of its own microtasks inside
    // Reka's handler, so the emit lands a tick or two after the key event.
    await waitFor(() => expect(view.emitted('update:modelValue')).toBeTruthy())
    expect(view.emitted('update:modelValue').at(-1)).toEqual(['blue'])
  })

  it('refuses an option that is disabled', async () => {
    standalone()
    const trigger = screen.getByRole('combobox', { name: 'Colour' })

    await fireEvent.keyDown(trigger, { key: 'ArrowDown' })

    expect(screen.getByRole('option', { name: 'Gold' })).toHaveAttribute('aria-disabled', 'true')
    expect(screen.getByRole('option', { name: 'Red' })).not.toHaveAttribute('aria-disabled')
  })

  it('does not open while disabled', async () => {
    standalone({ disabled: true })

    const trigger = screen.getByRole('combobox', { name: 'Colour' })
    expect(trigger).toBeDisabled()

    await fireEvent.keyDown(trigger, { key: 'ArrowDown' })

    expect(screen.queryByRole('option')).toBeNull()
  })

  it('takes its label, its description and its invalid state from the field', () => {
    const composed = defineComponent({
      setup() {
        return () =>
          h(
            GovueField,
            { label: 'Colour', error: 'Pick one.' },
            { default: () => h(GovueSelect, { options }) },
          )
      },
    })
    render(composed)

    const select = screen.getByLabelText('Colour')
    expect(select).toHaveAttribute('aria-invalid', 'true')
    expect(select).toHaveAccessibleDescription('Pick one.')
  })

  it('is a touch sized control', () => {
    standalone()

    expect(screen.getByRole('combobox', { name: 'Colour' }).className).toContain('min-h-11')
  })
})
