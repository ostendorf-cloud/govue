<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { inject, onBeforeUnmount, ref, useId, watch } from 'vue'

import { useGovueText } from '../i18n/plugin'
import { headerContextKey } from './headerContext'

export interface NavItem {
  href: string
  label: string
  /** The page the visitor is on. At most one item carries it. */
  current?: boolean
  /**
   * `cta` renders the item as a solid button, the call to action of the
   * row, in the nav row and in the open menu. It stays a link: it navigates.
   * Mark one item at most, or nothing stands out.
   */
  variant?: 'cta'
}

/** The classes of one item. Whole names, so Tailwind finds them. */
function itemClasses(item: NavItem): string[] {
  if (item.variant === 'cta') {
    return [
      'inline-flex min-h-11 w-full items-center justify-center rounded-md px-4 text-base font-medium transition-colors sm:w-auto',
      'bg-accent-strong text-accent-on hover:bg-accent-strong-hover',
      // The page is also said by aria-current, not by the fill alone.
      item.current ? 'underline decoration-2 underline-offset-4' : '',
    ]
  }
  return [
    'inline-flex min-h-11 w-full items-center rounded-md px-3 text-base transition-colors sm:w-auto',
    item.current
      ? 'bg-accent-subtle font-semibold text-accent-text underline decoration-2 underline-offset-4'
      : 'text-text hover:bg-surface-hover',
  ]
}

withDefaults(
  defineProps<{
    items: NavItem[]
    /** Every navigation on a page needs its own name. */
    ariaLabel?: string
    /** The accessible name of the button that opens the collapsed list. */
    toggleLabel?: string
  }>(),
  { ariaLabel: undefined, toggleLabel: undefined },
)

const { t } = useGovueText()

const listId = useId()
const open = ref(false)
const toggle = ref<HTMLButtonElement | null>(null)

// Inside a GovueHeader the header learns whether the list is open, so an
// overlay header can give the open menu a solid ground. Outside one this is
// a no-op.
const header = inject(headerContextKey, null)
watch(open, (value) => header?.setMenuOpen(value))
onBeforeUnmount(() => header?.setMenuOpen(false))

// From 480px the header is one row of --spacing-header, too low for the open
// list. Between 480 and 639px the list therefore leaves the flow and drops
// below the bar, over the page, full width, on the ground of the header. Its
// containing block is the header element, which is sticky or fixed: nothing
// between them is positioned. top-full is the top of the header's bottom
// border, and the list's own top border lies on it. It scrolls when it is
// taller than the window below the bar. Below 480px the list stays in the
// flow, on the navigation row, and from 640px it is the inline row. Outside a
// header the list always stays in the flow. Whole class names, so Tailwind
// finds them.
const dropBelowBar = header
  ? 'min-[480px]:max-sm:absolute min-[480px]:max-sm:inset-x-0 min-[480px]:max-sm:top-full min-[480px]:max-sm:max-h-[calc(100dvh-var(--spacing-header))] min-[480px]:max-sm:overflow-y-auto min-[480px]:max-sm:border-y min-[480px]:max-sm:border-header-border min-[480px]:max-sm:bg-header-bg min-[480px]:max-sm:px-4'
  : ''

/** close returns focus to the toggle, so Escape does not drop the visitor at
 *  the top of the page. */
function close(): void {
  if (!open.value) {
    return
  }
  open.value = false
  toggle.value?.focus()
}
</script>

<template>
  <nav :aria-label="ariaLabel ?? t('govue.nav.label')" @keydown.escape="close">
    <!-- Only below sm: from there the list is always on screen and the button
         would be a second way to do nothing. -->
    <button
      ref="toggle"
      type="button"
      :aria-expanded="open ? 'true' : 'false'"
      :aria-controls="listId"
      class="inline-flex min-h-11 items-center gap-2 rounded-md border border-border-strong bg-surface px-3 text-base font-medium text-text transition-colors hover:bg-surface-hover sm:hidden"
      @click="open = !open"
    >
      <svg class="size-4 shrink-0" viewBox="0 0 16 16" aria-hidden="true" focusable="false">
        <path
          d="M2 4h12M2 8h12M2 12h12"
          fill="none"
          stroke="currentColor"
          stroke-width="1.5"
          stroke-linecap="round"
        />
      </svg>
      {{ toggleLabel ?? t('govue.nav.menu') }}
    </button>

    <ul
      :id="listId"
      :class="[
        'gap-1 py-2 sm:flex sm:flex-row sm:items-center sm:py-0',
        open ? 'flex flex-col' : 'hidden',
        dropBelowBar,
      ]"
    >
      <li v-for="item in items" :key="item.href">
        <!-- A click on a link on a phone leaves the page, so the open list must
             not survive it. -->
        <a
          :href="item.href"
          :aria-current="item.current ? 'page' : undefined"
          :data-variant="item.variant"
          :class="itemClasses(item)"
          @click="close"
        >
          {{ item.label }}
        </a>
      </li>
    </ul>
  </nav>
</template>
