import { render, screen } from '@testing-library/vue'
import { describe, expect, it } from 'vitest'

import WkFooter from './WkFooter.vue'

const links = [
  { href: '#imprint', label: 'Imprint' },
  { href: '#privacy', label: 'Privacy' },
]

describe('WkFooter', () => {
  it('is the contentinfo landmark', () => {
    render(WkFooter)

    expect(screen.getByRole('contentinfo')).toBeVisible()
  })

  it('names its navigation, so it is not confused with the main one', () => {
    render(WkFooter, { props: { links } })

    expect(screen.getByRole('navigation', { name: 'Footer' })).toBeVisible()
  })

  it('renders the legal links to their destinations', () => {
    render(WkFooter, { props: { links } })

    expect(screen.getByRole('link', { name: 'Imprint' })).toHaveAttribute('href', '#imprint')
    expect(screen.getByRole('link', { name: 'Privacy' })).toHaveAttribute('href', '#privacy')
  })

  it('has no navigation when there are no links', () => {
    render(WkFooter, { props: { note: '© 2026. All rights reserved.' } })

    expect(screen.queryByRole('navigation')).toBeNull()
    expect(screen.getByText('© 2026. All rights reserved.')).toBeVisible()
  })

  it('shows what is put inside it', () => {
    render(WkFooter, { slots: { default: 'Need help? Our support team can take it from here.' } })

    expect(screen.getByText('Need help? Our support team can take it from here.')).toBeVisible()
  })

  it('places every link as a real, focusable control in document order', () => {
    render(WkFooter, { props: { links } })

    const imprint = screen.getByRole('link', { name: 'Imprint' })
    const privacy = screen.getByRole('link', { name: 'Privacy' })

    for (const link of [imprint, privacy]) {
      expect(link).not.toHaveAttribute('tabindex', '-1')
    }
    expect(
      Boolean(imprint.compareDocumentPosition(privacy) & Node.DOCUMENT_POSITION_FOLLOWING),
    ).toBe(true)

    imprint.focus()
    expect(imprint).toHaveFocus()
    privacy.focus()
    expect(privacy).toHaveFocus()
  })
})
