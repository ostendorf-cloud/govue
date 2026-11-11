import { fireEvent, render, screen } from '@testing-library/vue'
import { describe, expect, it } from 'vitest'

import WkInput from './WkInput.vue'

type InputProps = InstanceType<typeof WkInput>['$props']

/**
 * standalone renders the control on its own. The name then comes from an
 * attribute, which is why it is passed as one; inside a field the label
 * supplies it.
 */
function standalone(props: Partial<InputProps> = {}, label = 'Title') {
  return render(WkInput, { props, attrs: { 'aria-label': label } })
}

describe('WkInput', () => {
  it('writes what the user types back into the model', async () => {
    const { emitted } = standalone({ modelValue: '' })

    await fireEvent.update(screen.getByRole('textbox', { name: 'Title' }), 'Lamp')

    expect(emitted('update:modelValue').at(-1)).toEqual(['Lamp'])
  })

  it('shows the value it was given', () => {
    standalone({ modelValue: 'Lamp' })

    expect(screen.getByRole('textbox', { name: 'Title' })).toHaveValue('Lamp')
  })

  it('takes a type that changes the keyboard', () => {
    standalone({ type: 'email', inputmode: 'email', autocomplete: 'email' }, 'Email')

    const input = screen.getByRole('textbox', { name: 'Email' })
    expect(input).toHaveAttribute('type', 'email')
    expect(input).toHaveAttribute('inputmode', 'email')
    expect(input).toHaveAttribute('autocomplete', 'email')
  })

  it('marks itself invalid on its own as well', () => {
    standalone({ invalid: true })

    const input = screen.getByRole('textbox', { name: 'Title' })
    expect(input).toHaveAttribute('aria-invalid', 'true')
    expect(input.className).toContain('border-danger-strong')
  })

  it('reflects disabled and readonly as plain attributes', () => {
    standalone({ disabled: true, readonly: true })

    const input = screen.getByRole('textbox', { name: 'Title' })
    expect(input).toBeDisabled()
    expect(input).toHaveAttribute('readonly')
  })

  it('is a touch sized control', () => {
    standalone()

    expect(screen.getByRole('textbox').className).toContain('min-h-11')
  })
})
