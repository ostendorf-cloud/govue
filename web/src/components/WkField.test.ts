// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { render, screen } from '@testing-library/vue'
import { describe, expect, it } from 'vitest'
import { defineComponent, h } from 'vue'

import WkField from './WkField.vue'
import WkInput from './WkInput.vue'

type FieldProps = InstanceType<typeof WkField>['$props']

/** fieldWithInput is the normal pairing: the field labels the input. */
function fieldWithInput(props: FieldProps) {
  return defineComponent({
    setup() {
      return () => h(WkField, props, { default: () => h(WkInput, { placeholder: 'Lamp' }) })
    },
  })
}

describe('WkField', () => {
  it('labels the control inside it', () => {
    render(fieldWithInput({ label: 'Title' }))

    // Found by its label, which is what a screen reader and a user both use.
    expect(screen.getByLabelText('Title')).toBeVisible()
  })

  it('describes the control with its hint', () => {
    render(fieldWithInput({ label: 'Title', hint: 'Shown to your guests' }))

    const input = screen.getByLabelText('Title')
    expect(input).toHaveAccessibleDescription('Shown to your guests')
    expect(input).not.toHaveAttribute('aria-invalid')
  })

  it('announces the error, marks the control invalid and keeps the hint', () => {
    render(
      fieldWithInput({
        label: 'Title',
        hint: 'Shown to your guests',
        error: 'Please enter a title.',
      }),
    )

    const input = screen.getByLabelText('Title')
    expect(input).toHaveAttribute('aria-invalid', 'true')
    expect(input).toHaveAccessibleDescription('Shown to your guests Please enter a title.')
    expect(screen.getByRole('alert')).toHaveTextContent('Please enter a title.')
  })

  it('says that a field is required, in words', () => {
    render(fieldWithInput({ label: 'Title', required: true }))

    const input = screen.getByLabelText(/Title/)
    expect(input).toBeRequired()
    expect(screen.getByText('(required)')).toBeVisible()
  })

  it('disables the control through the field', () => {
    render(fieldWithInput({ label: 'Title', disabled: true }))

    expect(screen.getByLabelText('Title')).toBeDisabled()
  })

  it('hands the wiring to a custom control through the slot', () => {
    const custom = defineComponent({
      setup() {
        return () =>
          h(
            WkField,
            { label: 'Colour', error: 'Pick one.' },
            {
              default: ({
                id,
                describedBy,
                invalid,
              }: {
                id: string
                describedBy?: string
                invalid: boolean
              }) =>
                h('input', {
                  id,
                  'aria-describedby': describedBy,
                  'aria-invalid': invalid ? 'true' : undefined,
                }),
            },
          )
      },
    })
    render(custom)

    const input = screen.getByLabelText('Colour')
    expect(input).toHaveAttribute('aria-invalid', 'true')
    expect(input).toHaveAccessibleDescription('Pick one.')
  })

  it('gives two fields on one page different ids', () => {
    const two = defineComponent({
      setup() {
        return () =>
          h('div', [
            h(WkField, { label: 'First' }, { default: () => h(WkInput) }),
            h(WkField, { label: 'Second' }, { default: () => h(WkInput) }),
          ])
      },
    })
    render(two)

    const first = screen.getByLabelText('First')
    const second = screen.getByLabelText('Second')
    expect(first.id).not.toBe(second.id)
    expect(first.id).toBeTruthy()
  })
})
