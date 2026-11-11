import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useToasts } from './useToasts'

describe('useToasts', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    useToasts().dismissAll()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('raises a toast and dismisses a success on its own', () => {
    const { toasts, notify } = useToasts()

    notify({ kind: 'success', title: 'Changes saved' })
    expect(toasts.value).toHaveLength(1)
    expect(toasts.value[0].title).toBe('Changes saved')

    vi.advanceTimersByTime(4000)
    expect(toasts.value).toHaveLength(0)
  })

  it('keeps a failure until it is dismissed', () => {
    const { toasts, notify, dismiss } = useToasts()

    const id = notify({ kind: 'error', title: 'Saving failed', message: 'Request req-12' })

    vi.advanceTimersByTime(60_000)
    expect(toasts.value).toHaveLength(1)

    dismiss(id)
    expect(toasts.value).toHaveLength(0)
  })

  it('takes an explicit timeout', () => {
    const { toasts, notify } = useToasts()

    notify({ kind: 'error', title: 'Gone in a second', timeoutMs: 1000 })
    vi.advanceTimersByTime(1000)

    expect(toasts.value).toHaveLength(0)
  })

  it('keeps at most four, dropping the oldest', () => {
    const { toasts, notify } = useToasts()

    for (const title of ['one', 'two', 'three', 'four', 'five']) {
      notify({ kind: 'error', title })
    }

    expect(toasts.value).toHaveLength(4)
    expect(toasts.value.map((toast) => toast.title)).toEqual(['two', 'three', 'four', 'five'])
  })

  it('shares the list across callers', () => {
    const first = useToasts()
    const second = useToasts()

    first.notify({ kind: 'info', title: 'Shared' })

    expect(second.toasts.value).toHaveLength(1)
  })
})
