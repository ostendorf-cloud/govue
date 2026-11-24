// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { render } from '@testing-library/vue'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, nextTick, ref, type Ref } from 'vue'

import { DEFAULT_SCROLL_THRESHOLD, useScrolled, type ScrolledOptions } from './useScrolled'

function scrollTo(y: number): void {
  Object.defineProperty(window, 'scrollY', { value: y, configurable: true, writable: true })
  window.dispatchEvent(new Event('scroll'))
}

/** host mounts a component that calls the composable and shows its state. */
function host(options?: ScrolledOptions) {
  let state!: Readonly<Ref<boolean>>
  const view = render(
    defineComponent({
      setup() {
        state = useScrolled(options)
        return () => h('p', { 'data-testid': 'state' }, String(state.value))
      },
    }),
  )
  return { state: () => state.value, view }
}

describe('useScrolled', () => {
  afterEach(() => {
    vi.restoreAllMocks()
    Object.defineProperty(window, 'scrollY', { value: 0, configurable: true, writable: true })
  })

  it('is false at the top and flips once the page passes the threshold', async () => {
    const { state } = host()
    expect(state()).toBe(false)

    scrollTo(DEFAULT_SCROLL_THRESHOLD)
    await nextTick()
    expect(state()).toBe(false)

    scrollTo(DEFAULT_SCROLL_THRESHOLD + 1)
    await nextTick()
    expect(state()).toBe(true)

    scrollTo(0)
    await nextTick()
    expect(state()).toBe(false)
  })

  it('takes its own threshold', async () => {
    const { state } = host({ threshold: 100 })

    scrollTo(50)
    await nextTick()
    expect(state()).toBe(false)
    scrollTo(101)
    await nextTick()
    expect(state()).toBe(true)
  })

  it('reads the offset at mount, so a reload mid page starts in the right state', () => {
    Object.defineProperty(window, 'scrollY', { value: 500, configurable: true, writable: true })
    const { state } = host()

    expect(state()).toBe(true)
  })

  it('listens passively, so scrolling is never blocked', () => {
    const add = vi.spyOn(window, 'addEventListener')
    host()

    const call = add.mock.calls.find(([type]) => type === 'scroll')
    expect(call?.[2]).toEqual({ passive: true })
  })

  it('does no render work while the state stays the same', async () => {
    let renders = 0
    render(
      defineComponent({
        setup() {
          const scrolled = useScrolled()
          return () => {
            renders++
            return h('p', String(scrolled.value))
          }
        },
      }),
    )
    const before = renders

    for (const y of [20, 40, 80, 400, 900]) {
      scrollTo(y)
    }
    await nextTick()

    // One render for the flip, none for the four events that changed nothing.
    expect(renders).toBe(before + 1)
  })

  it('removes the listener when the component goes away', () => {
    const remove = vi.spyOn(window, 'removeEventListener')
    const { view } = host()

    view.unmount()

    expect(remove.mock.calls.some(([type]) => type === 'scroll')).toBe(true)
  })

  it('attaches nothing while it is not active, and follows the switch', async () => {
    const add = vi.spyOn(window, 'addEventListener')
    const active = ref(false)
    const { state } = host({ active })

    expect(add.mock.calls.some(([type]) => type === 'scroll')).toBe(false)
    scrollTo(300)
    await nextTick()
    expect(state()).toBe(false)

    active.value = true
    await nextTick()
    expect(state()).toBe(true)

    active.value = false
    await nextTick()
    expect(state()).toBe(false)
    // Scrolling after the switch went off changes nothing.
    scrollTo(900)
    await nextTick()
    expect(state()).toBe(false)
  })
})
