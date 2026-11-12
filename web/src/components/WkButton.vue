<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { computed } from 'vue'

type Variant = 'primary' | 'secondary' | 'quiet' | 'danger'

const props = withDefaults(
  defineProps<{
    variant?: Variant
    type?: 'button' | 'submit' | 'reset'
    disabled?: boolean
    /** In flight: the button is disabled, says so, and shows a spinner. */
    loading?: boolean
    /** What a screen reader hears while loading. */
    loadingLabel?: string
    /** Fills the width, for a form's submit on a phone. */
    block?: boolean
  }>(),
  {
    variant: 'secondary',
    type: 'button',
    loadingLabel: 'Working',
  },
)

const inert = computed(() => props.disabled || props.loading)

const variants: Record<Variant, string> = {
  primary: 'bg-accent-strong text-accent-on hover:bg-accent-strong-hover',
  secondary: 'bg-surface text-text border border-border-strong hover:bg-surface-hover',
  quiet: 'bg-transparent text-accent-text hover:bg-accent-subtle',
  danger: 'bg-danger-strong text-danger-on hover:bg-danger-strong-hover',
}

const classes = computed(() => [
  // 44px on every variant: a size variant changes padding and font size, not
  // the touch target.
  'inline-flex min-h-11 items-center justify-center gap-2 rounded-md px-4 text-base font-medium transition-colors',
  variants[props.variant],
  props.block ? 'w-full' : '',
  inert.value ? 'pointer-events-none opacity-60' : '',
])
</script>

<template>
  <button
    :type="type"
    :class="classes"
    :disabled="inert || undefined"
    :aria-busy="loading ? 'true' : undefined"
  >
    <!-- The spinner keeps its own box, so the label does not shift when it
         appears. prefers-reduced-motion in base.css stops it turning and
         leaves it as a plain marker. -->
    <svg
      v-if="loading"
      class="size-4 shrink-0 animate-spin"
      viewBox="0 0 16 16"
      aria-hidden="true"
      focusable="false"
    >
      <circle
        cx="8"
        cy="8"
        r="6.5"
        fill="none"
        stroke="currentColor"
        stroke-opacity="0.25"
        stroke-width="2"
      />
      <path
        d="M14.5 8a6.5 6.5 0 0 0-6.5-6.5"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
      />
    </svg>
    <span v-if="loading" class="sr-only">{{ loadingLabel }}</span>
    <slot />
  </button>
</template>
