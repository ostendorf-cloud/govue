import { fireEvent, render, screen } from '@testing-library/vue'
import { describe, expect, it } from 'vitest'
import { defineComponent, h } from 'vue'

import WkField from './WkField.vue'
import WkTextarea from './WkTextarea.vue'

type TextareaProps = InstanceType<typeof WkTextarea>['$props']
type FieldProps = InstanceType<typeof WkField>['$props']

/**
 * standalone renders the control on its own. The name then comes from an
 * attribute, which is why it is passed as one; inside a field the label
 * supplies it.
 */
function standalone(props: Partial<TextareaProps> = {}) {
  return render(WkTextarea, { props, attrs: { 'aria-label': 'Notes' } })
}

/** fieldWithTextarea is the normal pairing: the field labels the textarea. */
function fieldWithTextarea(fieldProps: FieldProps, textareaProps: Partial<TextareaProps> = {}) {
  return defineComponent({
    setup() {
      return () => h(WkField, fieldProps, { default: () => h(WkTextarea, textareaProps) })
    },
  })
}

describe('WkTextarea', () => {
  it('writes what the user types back into the model', async () => {
    const view = standalone({ modelValue: '' })

    await fireEvent.update(screen.getByRole('textbox', { name: 'Notes' }), 'Bring a lamp')

    expect(view.emitted('update:modelValue').at(-1)).toEqual(['Bring a lamp'])
  })

  it('shows the value it was given', () => {
    standalone({ modelValue: 'Bring a lamp' })

    expect(screen.getByRole('textbox', { name: 'Notes' })).toHaveValue('Bring a lamp')
  })

  it('is four lines unless it is asked for more', async () => {
    const view = standalone()
    expect(screen.getByRole('textbox', { name: 'Notes' })).toHaveAttribute('rows', '4')

    await view.rerender({ rows: 8 })

    expect(screen.getByRole('textbox', { name: 'Notes' })).toHaveAttribute('rows', '8')
  })

  it('counts down what is left of the maximum', async () => {
    standalone({ maxlength: 10 })

    const textarea = screen.getByRole('textbox', { name: 'Notes' })
    expect(textarea).toHaveAccessibleDescription('10 characters left')

    await fireEvent.update(textarea, 'Lamp')

    expect(textarea).toHaveAccessibleDescription('6 characters left')
    expect(textarea).toHaveAttribute('maxlength', '10')
  })

  it('has no count to describe without a maximum', () => {
    standalone()

    expect(screen.getByRole('textbox', { name: 'Notes' })).not.toHaveAttribute('aria-describedby')
  })

  it('reflects disabled and readonly as plain attributes', () => {
    standalone({ disabled: true, readonly: true })

    const textarea = screen.getByRole('textbox', { name: 'Notes' })
    expect(textarea).toBeDisabled()
    expect(textarea).toHaveAttribute('readonly')
  })

  it('takes its label, its description and its invalid state from the field', () => {
    render(fieldWithTextarea({ label: 'Notes', error: 'Please say something.' }, { maxlength: 20 }))

    const textarea = screen.getByLabelText('Notes')
    expect(textarea).toHaveAttribute('aria-invalid', 'true')
    expect(textarea).toHaveAccessibleDescription('Please say something. 20 characters left')
  })

  it('is disabled by the field around it', () => {
    render(fieldWithTextarea({ label: 'Notes', disabled: true }))

    expect(screen.getByLabelText('Notes')).toBeDisabled()
  })
})
