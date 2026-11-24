// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { render, screen } from '@testing-library/vue'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'

import { THEME_STORAGE_KEY } from '../composables/themeStorage'
import { resetThemeForTests } from '../composables/useTheme'
import { createGovueI18n } from '../i18n/plugin'
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

    it('keeps the group name static and states the mode through the checked option', async () => {
      const user = userEvent.setup()
      render(GovueThemeSwitch, { props: { compact: true } })

      expect(screen.getByRole('radiogroup', { name: 'Theme' })).toBeVisible()
      await user.click(screen.getByRole('radio', { name: 'Dark mode' }))
      expect(screen.getByRole('radiogroup', { name: 'Theme' })).toBeVisible()
      expect(screen.getByRole('radio', { name: 'Dark mode' })).toBeChecked()
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

      expect(screen.getByRole('radiogroup', { name: 'Design' })).toBeVisible()
      expect(screen.getByRole('radio', { name: 'Nacht' })).toBeVisible()
    })
  })

  describe('cycle', () => {
    const button = () => screen.getByRole('button')

    it('is one plain button with the icon of the current mode, no radio group', () => {
      render(GovueThemeSwitch, { props: { cycle: true } })

      expect(screen.getAllByRole('button')).toHaveLength(1)
      expect(button()).toHaveAttribute('type', 'button')
      expect(screen.queryByRole('radiogroup')).not.toBeInTheDocument()
      expect(screen.queryByRole('radio')).not.toBeInTheDocument()
      expect(button().textContent?.trim()).toBe('')
      const svgs = button().querySelectorAll('svg')
      expect(svgs).toHaveLength(1)
      expect(svgs[0]).toHaveAttribute('aria-hidden', 'true')
      expect(button()).toHaveAttribute('data-mode', 'system')
    })

    it('shows the icon of the compact radio of the same mode', async () => {
      const user = userEvent.setup()
      const { unmount } = render(GovueThemeSwitch, { props: { compact: true } })
      const icons = new Map(
        screen
          .getAllByRole('radio')
          .map((r) => [r.dataset.mode, r.querySelector('svg')?.outerHTML]),
      )
      unmount()
      render(GovueThemeSwitch, { props: { cycle: true } })

      for (let i = 0; i < 3; i++) {
        expect(button().querySelector('svg')?.outerHTML).toBe(icons.get(button().dataset.mode))
        await user.click(button())
      }
    })

    it('steps system, light, dark and back to system, applying and storing each', async () => {
      const user = userEvent.setup()
      render(GovueThemeSwitch, { props: { cycle: true } })
      const stored = () => JSON.parse(localStorage.getItem(THEME_STORAGE_KEY) ?? '{}').mode

      expect(button()).toHaveAttribute('data-mode', 'system')
      await user.click(button())
      expect(button()).toHaveAttribute('data-mode', 'light')
      expect(document.documentElement.dataset.mode).toBe('light')
      expect(stored()).toBe('light')
      await user.click(button())
      expect(button()).toHaveAttribute('data-mode', 'dark')
      expect(document.documentElement.dataset.mode).toBe('dark')
      expect(stored()).toBe('dark')
      await user.click(button())
      expect(button()).toHaveAttribute('data-mode', 'system')
      expect(document.documentElement.dataset.mode).toBeUndefined()
      expect(stored()).toBe('system')
    })

    it('names the current mode and the next one, and updates the name on each step', async () => {
      const user = userEvent.setup()
      render(GovueThemeSwitch, { props: { cycle: true } })

      expect(button()).toHaveAccessibleName('Theme: Follow the system. Next: Light mode')
      await user.click(button())
      expect(button()).toHaveAccessibleName('Theme: Light mode. Next: Dark mode')
      await user.click(button())
      expect(button()).toHaveAccessibleName('Theme: Dark mode. Next: Follow the system')
      await user.click(button())
      expect(button()).toHaveAccessibleName('Theme: Follow the system. Next: Light mode')
    })

    it('starts from a stored mode', () => {
      localStorage.setItem(THEME_STORAGE_KEY, JSON.stringify({ mode: 'dark' }))
      resetThemeForTests()
      render(GovueThemeSwitch, { props: { cycle: true } })

      expect(button()).toHaveAttribute('data-mode', 'dark')
      expect(button()).toHaveAccessibleName('Theme: Dark mode. Next: Follow the system')
    })

    it('is operated with Enter and Space, and keeps focus', async () => {
      const user = userEvent.setup()
      render(GovueThemeSwitch, { props: { cycle: true } })

      await user.tab()
      expect(button()).toHaveFocus()
      await user.keyboard('{Enter}')
      expect(button()).toHaveAttribute('data-mode', 'light')
      expect(button()).toHaveFocus()
      await user.keyboard(' ')
      expect(button()).toHaveAttribute('data-mode', 'dark')
      expect(button()).toHaveFocus()
    })

    it('is a 44 by 44px target with role tokens only and no ring of its own', () => {
      render(GovueThemeSwitch, { props: { cycle: true } })

      // size-11 is 2.75rem, 44px, border included; jsdom has no layout.
      expect(button()).toHaveClass('size-11', 'shrink-0', 'transition-colors')
      expect(button().className).not.toMatch(/#[0-9a-f]{3,8}|rgb|outline|ring/i)
    })

    it('takes the label and the mode names as props', () => {
      render(GovueThemeSwitch, {
        props: { cycle: true, label: 'Design', systemLabel: 'Auto', lightLabel: 'Hell' },
      })

      expect(button()).toHaveAccessibleName('Design: Auto. Next: Hell')
    })

    it('names the modes in German and in the formal German catalogue', () => {
      for (const formality of ['informal', 'formal'] as const) {
        resetThemeForTests()
        const { unmount } = render(GovueThemeSwitch, {
          props: { cycle: true },
          global: { plugins: [createGovueI18n({ locale: 'de', formality })] },
        })

        expect(button()).toHaveAccessibleName(
          'Darstellung: Dem System folgen. Als Nächstes: Heller Modus',
        )
        unmount()
      }
    })

    it('wins over compact', () => {
      render(GovueThemeSwitch, { props: { cycle: true, compact: true } })

      expect(screen.queryByRole('radiogroup')).not.toBeInTheDocument()
      expect(screen.getAllByRole('button')).toHaveLength(1)
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

  // The markup of the two radio layouts as released in v0.9.0. A site's
  // prerender and its own rules depend on it, so the cycle variant must not
  // change a byte of either.
  describe('pinned markup', () => {
    /** One element per line, so a change shows as a readable diff. */
    const lines = (html: string) => html.replaceAll('><', '>\n<')

    it('keeps the default layout', () => {
      const { container } = render(GovueThemeSwitch)

      expect(lines(container.innerHTML)).toMatchInlineSnapshot(`
        "<div role="radiogroup" aria-label="Theme" class="inline-flex gap-1 rounded-md border border-border-strong bg-surface">
        <button type="button" role="radio" aria-checked="true" tabindex="0" data-mode="system" class="inline-flex min-h-11 min-w-11 items-center justify-center rounded-md text-base transition-colors px-3 bg-accent-strong text-accent-on">System</button>
        <button type="button" role="radio" aria-checked="false" tabindex="-1" data-mode="light" class="inline-flex min-h-11 min-w-11 items-center justify-center rounded-md text-base transition-colors px-3 text-text hover:bg-surface-hover active:bg-bg-subtle">Light</button>
        <button type="button" role="radio" aria-checked="false" tabindex="-1" data-mode="dark" class="inline-flex min-h-11 min-w-11 items-center justify-center rounded-md text-base transition-colors px-3 text-text hover:bg-surface-hover active:bg-bg-subtle">Dark</button>
        </div>"
      `)
    })

    it('keeps the compact layout', () => {
      const { container } = render(GovueThemeSwitch, { props: { compact: true } })

      expect(lines(container.innerHTML)).toMatchInlineSnapshot(`
        "<div role="radiogroup" aria-label="Theme" class="inline-flex gap-1 rounded-md border border-border-strong bg-surface">
        <button type="button" role="radio" aria-checked="true" tabindex="0" aria-label="Follow the system" title="Follow the system" data-mode="system" class="inline-flex min-h-11 min-w-11 items-center justify-center rounded-md text-base transition-colors px-2 bg-accent-strong text-accent-on">
        <svg class="size-5 shrink-0" viewBox="0 0 24 24" aria-hidden="true" focusable="false">
        <rect x="3" y="4" width="18" height="12" rx="2" fill="none" stroke="currentColor" stroke-width="1.7">
        </rect>
        <path d="M8 20h8M12 16v4" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round">
        </path>
        </svg>
        </button>
        <button type="button" role="radio" aria-checked="false" tabindex="-1" aria-label="Light mode" title="Light mode" data-mode="light" class="inline-flex min-h-11 min-w-11 items-center justify-center rounded-md text-base transition-colors px-2 text-text hover:bg-surface-hover active:bg-bg-subtle">
        <svg class="size-5 shrink-0" viewBox="0 0 24 24" aria-hidden="true" focusable="false">
        <circle cx="12" cy="12" r="4.5" fill="none" stroke="currentColor" stroke-width="1.7">
        </circle>
        <path d="M12 2.5v2.5M12 19v2.5M4.2 4.2l1.8 1.8M18 18l1.8 1.8M2.5 12H5M19 12h2.5M4.2 19.8 6 18M18 6l1.8-1.8" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round">
        </path>
        </svg>
        </button>
        <button type="button" role="radio" aria-checked="false" tabindex="-1" aria-label="Dark mode" title="Dark mode" data-mode="dark" class="inline-flex min-h-11 min-w-11 items-center justify-center rounded-md text-base transition-colors px-2 text-text hover:bg-surface-hover active:bg-bg-subtle">
        <svg class="size-5 shrink-0" viewBox="0 0 24 24" aria-hidden="true" focusable="false">
        <path d="M20 14.5A8.5 8.5 0 0 1 9.5 4a8.5 8.5 0 1 0 10.5 10.5Z" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">
        </path>
        </svg>
        </button>
        </div>"
      `)
    })
  })
})
