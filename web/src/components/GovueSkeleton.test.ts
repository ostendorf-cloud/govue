// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { render } from '@testing-library/vue'
import { describe, expect, it } from 'vitest'

import GovueSkeleton from './GovueSkeleton.vue'

/** bars returns the placeholder bars of the rendered skeleton. */
function bars(container: Element): Element[] {
  return Array.from(container.querySelectorAll('.animate-pulse'))
}

describe('GovueSkeleton', () => {
  it('is not announced, so the caller owns the live region', () => {
    const { container } = render(GovueSkeleton)

    expect(container.firstElementChild).toHaveAttribute('aria-hidden', 'true')
  })

  it('stands in for three lines of text by default', () => {
    const { container } = render(GovueSkeleton)

    expect(bars(container)).toHaveLength(3)
  })

  it('takes the number of lines the content has', () => {
    const { container } = render(GovueSkeleton, { props: { lines: 5 } })

    expect(bars(container)).toHaveLength(5)
  })

  it('never renders fewer than one line', () => {
    const { container } = render(GovueSkeleton, { props: { lines: 0 } })

    expect(bars(container)).toHaveLength(1)
  })

  it('stops the last line short, the way a paragraph ends', () => {
    const { container } = render(GovueSkeleton)

    const rendered = bars(container)
    expect(rendered[0]?.className).toContain('w-full')
    expect(rendered.at(-1)?.className).toContain('w-2/3')
  })

  it('takes a width variant', () => {
    const { container } = render(GovueSkeleton, { props: { lines: 2, width: 'half' } })

    expect(bars(container)[0]?.className).toContain('w-1/2')
  })

  it('is one block for an avatar or an image', () => {
    const { container } = render(GovueSkeleton, { props: { rounded: true, width: 'narrow' } })

    const rendered = bars(container)
    expect(rendered).toHaveLength(1)
    expect(rendered[0]?.className).toContain('rounded-lg')
    expect(rendered[0]?.className).toContain('w-1/3')
  })

  it('pulses, and the base stylesheet stops that under reduced motion', () => {
    const { container } = render(GovueSkeleton, { props: { lines: 1 } })

    expect(bars(container)[0]?.className).toContain('animate-pulse')
  })
})
