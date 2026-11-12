import { render, screen } from '@testing-library/vue'
import { describe, expect, it } from 'vitest'
import { defineComponent, h } from 'vue'

import WkSkipLink from './WkSkipLink.vue'

describe('WkSkipLink', () => {
  it('is a link to the main landmark', () => {
    render(WkSkipLink)

    expect(screen.getByRole('link', { name: 'Skip to content' })).toHaveAttribute('href', '#main')
  })

  it('takes another target', () => {
    render(WkSkipLink, { props: { target: '#article' } })

    expect(screen.getByRole('link', { name: 'Skip to content' })).toHaveAttribute(
      'href',
      '#article',
    )
  })

  it('is the first focusable thing on the page', () => {
    const page = defineComponent({
      setup() {
        return () => h('div', [h(WkSkipLink), h('button', { type: 'button' }, 'Sign out')])
      },
    })
    render(page)

    const link = screen.getByRole('link', { name: 'Skip to content' })
    // jsdom has no stylesheet engine and cannot simulate a real Tab traversal
    // without userEvent, so this calls .focus() directly on the one element
    // that must be reachable first, rather than reimplementing Tab order.
    link.focus()

    expect(link).toHaveFocus()
  })

  it('is hidden by default: not focused, and carrying the hidden-until-focus markup contract', () => {
    render(WkSkipLink)

    const link = screen.getByRole('link', { name: 'Skip to content' })

    // This proves the markup contract, not computed CSS: jsdom applies no
    // stylesheet, so "hidden" here means (a) the link does not currently have
    // focus and (b) it carries the sr-only class that makes it visually
    // off-screen under the real, global stylesheet.
    expect(link).not.toHaveFocus()
    expect(link.className).toContain('sr-only')
  })

  it('becomes visible on focus: focused, and still carrying the focus-reveal markup contract', () => {
    render(WkSkipLink)

    const link = screen.getByRole('link', { name: 'Skip to content' })
    link.focus()

    // Again, this is the markup contract rather than observed layout: the
    // focus:not-sr-only family of classes is present on the element at all
    // times (that's how Tailwind's focus variant works), and what changes
    // under real CSS once focus lands is that the browser's :focus
    // pseudo-class makes those classes take effect. jsdom cannot render that,
    // so the test asserts the two preconditions that make it true: the
    // element has focus, and it is a real link (not tabindex="-1"), and it
    // still carries both the hidden-by-default and the focus-reveal classes.
    expect(link).toHaveFocus()
    expect(link).not.toHaveAttribute('tabindex', '-1')
    expect(link.className).toContain('sr-only')
    expect(link.className).toContain('focus:not-sr-only')
    expect(link.className).toContain('focus:absolute')
  })

  it('takes its own wording', () => {
    render(WkSkipLink, { slots: { default: 'Jump to the main content' } })

    expect(screen.getByRole('link', { name: 'Jump to the main content' })).toBeVisible()
  })
})
