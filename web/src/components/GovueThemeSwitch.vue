<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { ref } from 'vue'

import { useTheme, type ColorMode } from '../composables/useTheme'

// A radio group of three buttons: system, light and dark. Each option is a
// native button with role radio, so the focus ring is the global one on the
// element that has focus. Only the active option is in the tab order; the
// arrow keys move the choice, as a radio group does.
//
// The strings are props with English defaults, so the language layer can
// override them later without this component importing any i18n library.
//
// It shares the state of useTheme, and the first useTheme call decides the
// default theme and the storage key. An app that sets either calls
// useTheme({ ... }) in its setup before this component renders.
const props = withDefaults(
  defineProps<{
    /** The accessible name of the group. */
    label?: string
    systemLabel?: string
    lightLabel?: string
    darkLabel?: string
  }>(),
  {
    label: 'Theme',
    systemLabel: 'System',
    lightLabel: 'Light',
    darkLabel: 'Dark',
  },
)

const { mode, setMode } = useTheme()

const options = (): Array<{ value: ColorMode; text: string }> => [
  { value: 'system', text: props.systemLabel },
  { value: 'light', text: props.lightLabel },
  { value: 'dark', text: props.darkLabel },
]

const buttons = ref<HTMLButtonElement[]>([])

function choose(index: number): void {
  const all = options()
  const next = ((index % all.length) + all.length) % all.length
  setMode(all[next].value)
  buttons.value[next]?.focus()
}

function onKeydown(event: KeyboardEvent, index: number): void {
  switch (event.key) {
    case 'ArrowRight':
    case 'ArrowDown':
      choose(index + 1)
      break
    case 'ArrowLeft':
    case 'ArrowUp':
      choose(index - 1)
      break
    case 'Home':
      choose(0)
      break
    case 'End':
      choose(options().length - 1)
      break
    default:
      return
  }
  event.preventDefault()
}
</script>

<template>
  <div
    role="radiogroup"
    :aria-label="label"
    class="inline-flex gap-1 rounded-md border border-border-strong bg-surface"
  >
    <button
      v-for="(option, index) in options()"
      :key="option.value"
      ref="buttons"
      type="button"
      role="radio"
      :aria-checked="mode === option.value ? 'true' : 'false'"
      :tabindex="mode === option.value ? 0 : -1"
      :class="[
        'inline-flex min-h-11 min-w-11 items-center justify-center rounded-md px-3 text-base transition-colors',
        mode === option.value
          ? 'bg-accent-strong text-accent-on'
          : 'text-text hover:bg-surface-hover active:bg-bg-subtle',
      ]"
      @click="choose(index)"
      @keydown="onKeydown($event, index)"
    >
      {{ option.text }}
    </button>
  </div>
</template>
