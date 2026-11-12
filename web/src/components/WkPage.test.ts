import { render, screen } from '@testing-library/vue'
import { describe, expect, it } from 'vitest'

import WkPage from './WkPage.vue'

describe('WkPage', () => {
  it('is the main landmark the skip link points at', () => {
    render(WkPage, { slots: { default: 'Content' } })

    const main = screen.getByRole('main')
    expect(main).toBeVisible()
    expect(main).toHaveAttribute('id', 'main')
  })

  it('renders the title as the page heading', () => {
    render(WkPage, { props: { title: 'Your projects', lead: 'Everything you keep here.' } })

    expect(screen.getByRole('heading', { level: 1, name: 'Your projects' })).toBeVisible()
    expect(screen.getByText('Everything you keep here.')).toBeVisible()
  })

  it('has no heading when the page brings its own', () => {
    render(WkPage, { slots: { default: 'Content' } })

    expect(screen.queryByRole('heading')).toBeNull()
  })

  it('shows what is put inside it', () => {
    render(WkPage, { slots: { default: 'A list of recent entries' } })

    expect(screen.getByText('A list of recent entries')).toBeVisible()
  })

  it('holds the reading measure by default and the shell width on request', async () => {
    const page = render(WkPage)
    expect(screen.getByRole('main').className).toContain('max-w-reading')

    await page.rerender({ width: 'shell' })

    const main = screen.getByRole('main')
    expect(main.className).toContain('max-w-shell')
    // Side padding at every width, so nothing touches the edge of a phone.
    expect(main.className).toContain('px-4')
  })
})
