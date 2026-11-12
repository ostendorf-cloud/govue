<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

/** The five states a row, a job or a record can be in. */
export type BadgeKind = 'neutral' | 'success' | 'warning' | 'danger' | 'info'

const props = withDefaults(defineProps<{ kind?: BadgeKind }>(), { kind: 'neutral' })

// The tint pairs with the text colour of the same role. The word in the slot
// carries the meaning, the colour only repeats it.
const kinds: Record<BadgeKind, string> = {
  neutral: 'bg-bg-subtle text-text',
  success: 'bg-success-subtle text-success-text',
  warning: 'bg-warning-subtle text-warning-text',
  danger: 'bg-danger-subtle text-danger-text',
  info: 'bg-info-subtle text-info-text',
}
</script>

<template>
  <span
    :class="[
      'inline-flex items-center gap-1 rounded-sm px-2 py-0.5 text-xs font-medium',
      kinds[props.kind],
    ]"
  >
    <slot />
  </span>
</template>
