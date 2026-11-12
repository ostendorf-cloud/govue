<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Reka UI ships a Toast primitive, but this host stays hand-rolled on plain
// elements. A toast is not modal: there is no focus trap and no inert
// background to get right, which is exactly what made jsdom's blind spot a
// real problem for GovueDialog. What a toast actually has to get right -
// live-region politeness, the right text content, a working dismiss button,
// timeout behaviour - is genuinely verifiable under jsdom with fake timers,
// as this file's test suite shows. Adopting a second, heavier primitive here
// would add a ToastProvider and viewport wiring requirement without buying
// back any coverage jsdom could not already give.
import { computed } from 'vue'

import { useToasts, type Toast, type ToastKind } from '../composables/useToasts'
import GovueButton from './GovueButton.vue'

/** One live region and what belongs in it. */
interface Region {
  name: string
  role: 'status' | 'alert'
  live: 'polite' | 'assertive'
  items: Toast[]
}

const { toasts, dismiss } = useToasts()

const kindLabels: Record<ToastKind, string> = {
  success: 'Success',
  info: 'Info',
  error: 'Error',
}

// The word above the title is what carries the kind; the colour repeats it.
const kindClasses: Record<ToastKind, string> = {
  success: 'text-success-text',
  info: 'text-info-text',
  error: 'text-danger-text',
}

/**
 * Two regions, because politeness is a property of the region and not of the
 * message: a failure interrupts, everything else waits for a pause. Both are
 * in the DOM from the start, which is what makes an insertion announced at
 * all.
 */
const regions = computed<Region[]>(() => [
  {
    name: 'status',
    role: 'status',
    live: 'polite',
    items: toasts.value.filter((toast) => toast.kind !== 'error'),
  },
  {
    name: 'alert',
    role: 'alert',
    live: 'assertive',
    items: toasts.value.filter((toast) => toast.kind === 'error'),
  },
])
</script>

<template>
  <!-- Full width at the bottom on a phone, a column in the corner from the
       first breakpoint up. The strip itself takes no clicks, only the toasts
       in it do. -->
  <div
    class="pointer-events-none fixed inset-x-0 bottom-0 z-50 flex flex-col gap-2 p-4 sm:inset-x-auto sm:right-0 sm:w-96"
  >
    <div
      v-for="region in regions"
      :key="region.name"
      :role="region.role"
      :aria-live="region.live"
      class="flex flex-col gap-2"
    >
      <ul v-if="region.items.length > 0" class="flex flex-col gap-2">
        <li
          v-for="toast in region.items"
          :key="toast.id"
          class="pointer-events-auto flex items-start gap-2 rounded-md border border-border bg-surface p-3 text-text shadow-card"
        >
          <div class="flex min-w-0 flex-col gap-0.5">
            <p :class="['text-xs font-medium', kindClasses[toast.kind]]">
              {{ kindLabels[toast.kind] }}
            </p>
            <p class="text-sm font-semibold">{{ toast.title }}</p>
            <p v-if="toast.message" class="text-sm text-text-muted">{{ toast.message }}</p>
          </div>

          <GovueButton
            variant="quiet"
            class="ml-auto shrink-0"
            aria-label="Dismiss"
            @click="dismiss(toast.id)"
          >
            <svg class="size-4" viewBox="0 0 16 16" aria-hidden="true" focusable="false">
              <path
                d="m4 4 8 8M12 4l-8 8"
                fill="none"
                stroke="currentColor"
                stroke-width="2"
                stroke-linecap="round"
              />
            </svg>
          </GovueButton>
        </li>
      </ul>
    </div>
  </div>
</template>
