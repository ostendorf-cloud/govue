// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import {
  getCurrentScope,
  onMounted,
  onScopeDispose,
  ref,
  toValue,
  watch,
  type MaybeRefOrGetter,
  type Ref,
} from 'vue'

/** The distance in CSS pixels the page scrolls before the state flips. Small
 *  on purpose: the header turns solid on the first deliberate scroll, not on
 *  the jitter of a trackpad. */
export const DEFAULT_SCROLL_THRESHOLD = 16

export interface ScrolledOptions {
  /** The scroll offset in CSS pixels above which the page counts as scrolled.
   *  Default 16. */
  threshold?: number
  /** Listens only while this is true. A header that never overlays attaches no
   *  listener at all. Default true. */
  active?: MaybeRefOrGetter<boolean>
}

/**
 * useScrolled reports whether the window has scrolled past a threshold.
 *
 * The listener is passive and does no layout work: it reads `scrollY`, which
 * the browser already holds, compares it with the threshold and writes the
 * ref. A ref that gets the value it already has triggers nothing, so a scroll
 * event inside the same state costs one comparison and no render.
 *
 * Nothing runs on the server or before mount, so the state is false there and
 * a prerendered page starts in the state it was rendered in, until the client
 * app replaces the markup and reads the offset. At mount the
 * offset is read once, so a reload in the middle of a page starts in the right
 * state. The listener is removed with the component.
 *
 * Call it from a component's setup, or from any effect scope that is
 * mounted in a component.
 */
export function useScrolled(options: ScrolledOptions = {}): Readonly<Ref<boolean>> {
  const threshold = options.threshold ?? DEFAULT_SCROLL_THRESHOLD
  const scrolled = ref(false)
  let attached = false

  function update(): void {
    scrolled.value = window.scrollY > threshold
  }

  function attach(): void {
    if (attached) {
      return
    }
    attached = true
    update()
    window.addEventListener('scroll', update, { passive: true })
  }

  function detach(): void {
    if (!attached) {
      return
    }
    attached = false
    window.removeEventListener('scroll', update)
    scrolled.value = false
  }

  onMounted(() => {
    // The watcher starts after mount, so it never runs on the server.
    const stop = watch(
      () => toValue(options.active ?? true),
      (on) => (on ? attach() : detach()),
      { immediate: true },
    )
    if (getCurrentScope()) {
      onScopeDispose(stop)
    }
  })
  if (getCurrentScope()) {
    onScopeDispose(detach)
  }

  return scrolled
}
