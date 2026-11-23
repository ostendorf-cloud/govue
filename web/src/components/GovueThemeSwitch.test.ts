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

  describe('compact', () => {
    it('shows one icon per mode and no text, named by the mode', () => {
      render(GovueThemeSwitch, { props: { compact: true } })

      const radios = screen.getAllByRole('radio')
      expect(radios.map((r) => r.getAttribute('aria-label'))).toEqual([
        'Follow the system',
        'Light mode',
        'Dark mode',
      ])
      for (const radio of radios) {
        expect(radio.textContent?.trim()).toBe('')
        const svgs = radio.querySelectorAll('svg')
        expect(svgs).toHaveLength(1)
        expect(svgs[0]).toHaveAttribute('aria-hidden', 'true')
        expect(svgs[0].outerHTML).toContain('currentColor')
        expect(svgs[0].outerHTML).not.toMatch(/#[0-9a-f]{3,8}|rgb\(/i)
      }
      // The three icons differ.
      expect(new Set(radios.map((r) => r.querySelector('svg')?.innerHTML)).size).toBe(3)
    })

    it('states the current mode in the group name', async () => {
      const user = userEvent.setup()
      render(GovueThemeSwitch, { props: { compact: true } })

      expect(screen.getByRole('radiogroup', { name: 'Theme: Follow the system' })).toBeVisible()
      await user.click(screen.getByRole('radio', { name: 'Dark mode' }))
      expect(screen.getByRole('radiogroup', { name: 'Theme: Dark mode' })).toBeVisible()
      await user.click(screen.getByRole('radio', { name: 'Light mode' }))
      expect(screen.getByRole('radiogroup', { name: 'Theme: Light mode' })).toBeVisible()
    })

    it('applies and stores each mode', async () => {
      const user = userEvent.setup()
      render(GovueThemeSwitch, { props: { compact: true } })

      await user.click(screen.getByRole('radio', { name: 'Dark mode' }))
      expect(screen.getByRole('radio', { name: 'Dark mode' })).toBeChecked()
      expect(document.documentElement.dataset.mode).toBe('dark')
      expect(JSON.parse(localStorage.getItem(THEME_STORAGE_KEY) ?? '{}').mode).toBe('dark')

      await user.click(screen.getByRole('radio', { name: 'Light mode' }))
      expect(document.documentElement.dataset.mode).toBe('light')
      expect(JSON.parse(localStorage.getItem(THEME_STORAGE_KEY) ?? '{}').mode).toBe('light')

      await user.click(screen.getByRole('radio', { name: 'Follow the system' }))
      expect(screen.getByRole('radio', { name: 'Follow the system' })).toBeChecked()
      expect(JSON.parse(localStorage.getItem(THEME_STORAGE_KEY) ?? '{}').mode).toBe('system')
    })

    it('restores a persisted choice', () => {
      localStorage.setItem(THEME_STORAGE_KEY, JSON.stringify({ mode: 'dark' }))
      resetThemeForTests()
      render(GovueThemeSwitch, { props: { compact: true } })

      expect(screen.getByRole('radio', { name: 'Dark mode' })).toBeChecked()
      expect(screen.getByRole('radio', { name: 'Dark mode' })).toHaveAttribute('tabindex', '0')
      expect(screen.getByRole('radio', { name: 'Light mode' })).toHaveAttribute('tabindex', '-1')
    })

    it('keeps the arrow keys, Home, End and the roving tabindex', async () => {
      const user = userEvent.setup()
      render(GovueThemeSwitch, { props: { compact: true } })

      expect(screen.getByRole('radio', { name: 'Follow the system' })).toHaveAttribute(
        'tabindex',
        '0',
      )
      screen.getByRole('radio', { name: 'Follow the system' }).focus()
      await user.keyboard('{ArrowRight}')
      expect(screen.getByRole('radio', { name: 'Light mode' })).toBeChecked()
      expect(screen.getByRole('radio', { name: 'Light mode' })).toHaveFocus()
      await user.keyboard('{ArrowRight}')
      expect(screen.getByRole('radio', { name: 'Dark mode' })).toBeChecked()
      await user.keyboard('{ArrowRight}')
      expect(screen.getByRole('radio', { name: 'Follow the system' })).toBeChecked()
      await user.keyboard('{ArrowLeft}')
      expect(screen.getByRole('radio', { name: 'Dark mode' })).toBeChecked()
      await user.keyboard('{Home}')
      expect(screen.getByRole('radio', { name: 'Follow the system' })).toBeChecked()
      await user.keyboard('{End}')
      expect(screen.getByRole('radio', { name: 'Dark mode' })).toBeChecked()
    })

    it('makes every target at least 44px and about 142px wide in all', () => {
      render(GovueThemeSwitch, { props: { compact: true } })

      for (const radio of screen.getAllByRole('radio')) {
        expect(radio.className).toContain('min-h-11')
        expect(radio.className).toContain('min-w-11')
      }
      // 3 x 44px + 2 x 4px gap + 2 x 1px border, from the classes, not measured.
      const group = screen.getByRole('radiogroup')
      expect(group.className).toContain('gap-1')
      expect(group.className).toContain('border')
    })

    it('takes the option names as props', () => {
      render(GovueThemeSwitch, {
        props: { compact: true, label: 'Design', darkLabel: 'Nacht' },
      })

      expect(screen.getByRole('radiogroup', { name: 'Design: Follow the system' })).toBeVisible()
      expect(screen.getByRole('radio', { name: 'Nacht' })).toBeVisible()
    })
  })

  it('does not change the default layout', () => {
    render(GovueThemeSwitch)

    for (const radio of screen.getAllByRole('radio')) {
      expect(radio.querySelector('svg')).toBeNull()
      expect(radio).not.toHaveAttribute('aria-label')
    }
    expect(screen.getByRole('radiogroup', { name: 'Theme' })).toBeVisible()
  })

  it('makes every option at least 44px tall', () => {
    render(GovueThemeSwitch)

    for (const radio of screen.getAllByRole('radio')) {
      expect(radio.className).toContain('min-h-11')
      expect(radio.className).toContain('min-w-11')
    }
  })
})
