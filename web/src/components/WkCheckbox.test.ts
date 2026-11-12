import { fireEvent, render, screen } from '@testing-library/vue'
import { describe, expect, it } from 'vitest'

import WkCheckbox from './WkCheckbox.vue'

describe('WkCheckbox', () => {
  it('labels itself with its own text', () => {
    render(WkCheckbox, { props: { label: 'Send me the weekly mail' } })

    expect(screen.getByRole('checkbox', { name: 'Send me the weekly mail' })).toBeVisible()
  })

  it('writes the new state back into the model on a click', async () => {
    const view = render(WkCheckbox, { props: { label: 'Send me the weekly mail' } })

    await fireEvent.click(screen.getByRole('checkbox', { name: 'Send me the weekly mail' }))

    expect(view.emitted('update:modelValue').at(-1)).toEqual([true])
  })

  it('toggles from a click on the label text as well, since the row is the label', async () => {
    render(WkCheckbox, { props: { label: 'Send me the weekly mail' } })

    await fireEvent.click(screen.getByText('Send me the weekly mail'))

    expect(screen.getByRole('checkbox', { name: 'Send me the weekly mail' })).toBeChecked()
  })

  it('shows the state it was given', () => {
    render(WkCheckbox, { props: { label: 'Send me the weekly mail', modelValue: true } })

    expect(screen.getByRole('checkbox', { name: 'Send me the weekly mail' })).toBeChecked()
  })

  it('describes the choice with its hint', () => {
    render(WkCheckbox, {
      props: { label: 'Send me the weekly mail', hint: 'One mail every Monday' },
    })

    expect(
      screen.getByRole('checkbox', { name: 'Send me the weekly mail' }),
    ).toHaveAccessibleDescription('One mail every Monday')
  })

  it('is a disabled native checkbox, which the browser alone keeps from toggling', () => {
    render(WkCheckbox, { props: { label: 'Send me the weekly mail', disabled: true } })

    // A real click never reaches a disabled checkbox's toggling behaviour;
    // that is the platform's own guarantee, not something to re-implement or
    // re-test here, only the attribute that switches it on.
    expect(screen.getByRole('checkbox', { name: 'Send me the weekly mail' })).toBeDisabled()
  })

  it('has a hit area a finger reaches', () => {
    render(WkCheckbox, { props: { label: 'Send me the weekly mail' } })

    const row = screen.getByRole('checkbox', { name: 'Send me the weekly mail' }).closest('label')
    expect(row?.className).toContain('min-h-11')
  })
})
