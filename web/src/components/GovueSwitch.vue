<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// A plain button already gets Enter and Space activation and focus
// management for free, because that is what a native button is: this hand
// rolls the switch instead of reaching for Reka UI, since toggling the model
// on activation is the only behaviour a switch needs beyond that.
defineProps<{
  /** The visible text, which is also the switch's accessible name. */
  label: string
  disabled?: boolean
}>()

const model = defineModel<boolean>({ default: false })

function toggle(): void {
  model.value = !model.value
}
</script>

<template>
  <button
    type="button"
    role="switch"
    :aria-checked="model ? 'true' : 'false'"
    :disabled="disabled || undefined"
    :class="[
      'inline-flex min-h-11 items-center gap-3 rounded-md px-2 text-base text-text transition-colors',
      disabled ? 'cursor-not-allowed opacity-60' : 'hover:bg-surface-hover active:bg-bg-subtle',
    ]"
    @click="toggle"
  >
    <span>{{ label }}</span>

    <span
      :class="[
        'inline-flex h-6 w-11 shrink-0 items-center rounded-lg border border-border-strong transition-colors',
        model ? 'bg-accent-strong' : 'bg-bg-subtle',
      ]"
      aria-hidden="true"
    >
      <!-- Only the knob moves, and it moves by transform alone. -->
      <span
        :class="[
          'size-5 rounded-lg bg-surface transition-transform',
          model ? 'translate-x-5' : 'translate-x-0.5',
        ]"
      />
    </span>
  </button>
</template>
