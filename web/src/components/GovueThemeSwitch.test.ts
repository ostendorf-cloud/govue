// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { render, screen } from '@testing-library/vue'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'

import { THEME_STORAGE_KEY } from '../composables/themeStorage'
import { resetThemeForTests } from '../composables/useTheme'
import GovueThemeSwitch from './GovueThemeSwitch.vue'

describe('GovueThemeSwitch', () => {
  beforeEach(() => {
    vi.unstubAllGlobals()
    vi.stubGlobal('matchMedia', () => ({
      matches: false,
      addEventListener: () => {},
      removeEventListener: () => {},
    }))
    resetThemeForTests()
    localStorage.clear()
    delete document.documentElement.dataset.mode
  })

  it('is a named radio group with system, light and dark', () => {
    render(GovueThemeSwitch)

    const group = screen.getByRole('radiogroup', { name: 'Theme' })
    expect(group).toBeVisible()
    expect(screen.getAllByRole('radio').map((r) => r.textContent?.trim())).toEqual([
      'System',
      'Light',
      'Dark',
    ])
  })

  it('starts on the system setting', () => {
    render(GovueThemeSwitch)

    expect(screen.getByRole('radio', { name: 'System' })).toBeChecked()
    expect(screen.getByRole('radio', { name: 'Light' })).not.toBeChecked()
    expect(screen.getByRole('radio', { name: 'Dark' })).not.toBeChecked()
  })

  it('applies and stores a click, and announces the active state', async () => {
    const user = userEvent.setup()
    render(GovueThemeSwitch)

    await user.click(screen.getByRole('radio', { name: 'Dark' }))
    await nextTick()

    expect(screen.getByRole('radio', { name: 'Dark' })).toBeChecked()
    expect(screen.getByRole('radio', { name: 'System' })).not.toBeChecked()
    expect(document.documentElement.dataset.mode).toBe('dark')
    expect(JSON.parse(localStorage.getItem(THEME_STORAGE_KEY) ?? '{}').mode).toBe('dark')
  })

  it('keeps only the active option in the tab order', () => {
    render(GovueThemeSwitch)

    expect(screen.getByRole('radio', { name: 'System' })).toHaveAttribute('tabindex', '0')
    expect(screen.getByRole('radio', { name: 'Light' })).toHaveAttribute('tabindex', '-1')
    expect(screen.getByRole('radio', { name: 'Dark' })).toHaveAttribute('tabindex', '-1')
  })

  it('moves the choice with the arrow keys, Home and End', async () => {
    const user = userEvent.setup()
    render(GovueThemeSwitch)

    screen.getByRole('radio', { name: 'System' }).focus()
    await user.keyboard('{ArrowRight}')
    expect(screen.getByRole('radio', { name: 'Light' })).toBeChecked()
    expect(screen.getByRole('radio', { name: 'Light' })).toHaveFocus()

    await user.keyboard('{ArrowDown}')
    expect(screen.getByRole('radio', { name: 'Dark' })).toBeChecked()

    await user.keyboard('{ArrowRight}')
    expect(screen.getByRole('radio', { name: 'System' })).toBeChecked()

    await user.keyboard('{ArrowLeft}')
    expect(screen.getByRole('radio', { name: 'Dark' })).toBeChecked()

    await user.keyboard('{Home}')
    expect(screen.getByRole('radio', { name: 'System' })).toBeChecked()

    await user.keyboard('{End}')
    expect(screen.getByRole('radio', { name: 'Dark' })).toBeChecked()
  })

  it('takes every string as a prop', () => {
    render(GovueThemeSwitch, {
      props: { label: 'Design', systemLabel: 'Auto', lightLabel: 'Hell', darkLabel: 'Dunkel' },
    })

    expect(screen.getByRole('radiogroup', { name: 'Design' })).toBeVisible()
    expect(screen.getByRole('radio', { name: 'Auto' })).toBeVisible()
    expect(screen.getByRole('radio', { name: 'Hell' })).toBeVisible()
    expect(screen.getByRole('radio', { name: 'Dunkel' })).toBeVisible()
  })

  it('makes every option at least 44px tall', () => {
    render(GovueThemeSwitch)

    for (const radio of screen.getAllByRole('radio')) {
      expect(radio.className).toContain('min-h-11')
      expect(radio.className).toContain('min-w-11')
    }
  })
})
