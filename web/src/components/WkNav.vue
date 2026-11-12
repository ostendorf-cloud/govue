<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { ref, useId } from 'vue'

export interface NavItem {
  href: string
  label: string
  /** The page the visitor is on. At most one item carries it. */
  current?: boolean
}

withDefaults(
  defineProps<{
    items: NavItem[]
    /** Every navigation on a page needs its own name. */
    ariaLabel?: string
    /** The accessible name of the button that opens the collapsed list. */
    toggleLabel?: string
  }>(),
  { ariaLabel: 'Main', toggleLabel: 'Menu' },
)

const listId = useId()
const open = ref(false)
const toggle = ref<HTMLButtonElement | null>(null)

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
  <nav :aria-label="ariaLabel" @keydown.escape="close">
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
      {{ toggleLabel }}
    </button>

    <ul
      :id="listId"
      :class="[
        'gap-1 py-2 sm:flex sm:flex-row sm:items-center sm:py-0',
        open ? 'flex flex-col' : 'hidden',
      ]"
    >
      <li v-for="item in items" :key="item.href">
        <!-- A click on a link on a phone leaves the page, so the open list must
             not survive it. -->
        <a
          :href="item.href"
          :aria-current="item.current ? 'page' : undefined"
          :class="[
            'inline-flex min-h-11 w-full items-center rounded-md px-3 text-base transition-colors sm:w-auto',
            item.current
              ? 'bg-accent-subtle font-semibold text-accent-text underline decoration-2 underline-offset-4'
              : 'text-text hover:bg-surface-hover',
          ]"
          @click="close"
        >
          {{ item.label }}
        </a>
      </li>
    </ul>
  </nav>
</template>
