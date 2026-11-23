<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { ref } from 'vue'

import { useTheme, type ColorMode } from '../composables/useTheme'
import { useGovueText } from '../i18n/plugin'

// A radio group of three buttons: system, light and dark. Each option is a
// native button with role radio, so the focus ring is the global one on the
// element that has focus. Only the active option is in the tab order; the
// arrow keys move the choice, as a radio group does.
//
// The strings are props that override the catalogue. Left out, they come from
// the language layer, English when it is not installed.
//
// With compact set, each option is one inline SVG icon (a device for system, a
// sun for light, a moon for dark) in a square target of 44px. The group is the
// same radio group with the same keys. The options are named by the mode
// ("Light mode") and the group name ends in the current mode ("Theme: Light
// mode"), since an icon says nothing to a screen reader. The three targets
// take about 142px: 3 x 44px, two 4px gaps and the 1px border on both sides
// (computed from the classes, jsdom has no layout), against the 328px of a
// 360px phone header. The icons use currentColor, so the role tokens colour
// them, and the transition is dropped by the global reduced motion rule.
//
// It shares the state of useTheme, and the first useTheme call decides the
// default theme and the storage key. An app that sets either calls
// useTheme({ ... }) in its setup before this component renders.
const props = withDefaults(
  defineProps<{
    /** The accessible name of the group. In the compact variant the current mode is appended. */
    label?: string
    /** The name of an option: its text, or in the compact variant its accessible name. */
    systemLabel?: string
    lightLabel?: string
    darkLabel?: string
    /** One icon per mode instead of a text button. Off by default. */
    compact?: boolean
  }>(),
  {
    label: undefined,
    systemLabel: undefined,
    lightLabel: undefined,
    darkLabel: undefined,
    compact: false,
  },
)

const { mode, setMode } = useTheme()
const { t } = useGovueText()

const options = (): Array<{ value: ColorMode; text: string }> =>
  props.compact
    ? [
        { value: 'system', text: props.systemLabel ?? t('govue.theme.compact.system') },
        { value: 'light', text: props.lightLabel ?? t('govue.theme.compact.light') },
        { value: 'dark', text: props.darkLabel ?? t('govue.theme.compact.dark') },
      ]
    : [
        { value: 'system', text: props.systemLabel ?? t('govue.theme.system') },
        { value: 'light', text: props.lightLabel ?? t('govue.theme.light') },
        { value: 'dark', text: props.darkLabel ?? t('govue.theme.dark') },
      ]

const groupLabel = (): string => {
  const base = props.label ?? t('govue.theme.label')
  if (!props.compact) {
    return base
  }
  const current = options().find((option) => option.value === mode.value)
  return t('govue.theme.current', { label: base, mode: current?.text ?? '' })
}

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
    :aria-label="groupLabel()"
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
      :aria-label="compact ? option.text : undefined"
      :title="compact ? option.text : undefined"
      :data-mode="option.value"
      :class="[
        'inline-flex min-h-11 min-w-11 items-center justify-center rounded-md text-base transition-colors',
        compact ? 'px-2' : 'px-3',
        mode === option.value
          ? 'bg-accent-strong text-accent-on'
          : 'text-text hover:bg-surface-hover active:bg-bg-subtle',
      ]"
      @click="choose(index)"
      @keydown="onKeydown($event, index)"
    >
      <template v-if="!compact">{{ option.text }}</template>
      <svg
        v-else-if="option.value === 'system'"
        class="size-5 shrink-0"
        viewBox="0 0 24 24"
        aria-hidden="true"
        focusable="false"
      >
        <rect
          x="3"
          y="4"
          width="18"
          height="12"
          rx="2"
          fill="none"
          stroke="currentColor"
          stroke-width="1.7"
        />
        <path
          d="M8 20h8M12 16v4"
          fill="none"
          stroke="currentColor"
          stroke-width="1.7"
          stroke-linecap="round"
        />
      </svg>
      <svg
        v-else-if="option.value === 'dark'"
        class="size-5 shrink-0"
        viewBox="0 0 24 24"
        aria-hidden="true"
        focusable="false"
      >
        <path
          d="M20 14.5A8.5 8.5 0 0 1 9.5 4a8.5 8.5 0 1 0 10.5 10.5Z"
          fill="none"
          stroke="currentColor"
          stroke-width="1.7"
          stroke-linecap="round"
          stroke-linejoin="round"
        />
      </svg>
      <svg v-else class="size-5 shrink-0" viewBox="0 0 24 24" aria-hidden="true" focusable="false">
        <circle cx="12" cy="12" r="4.5" fill="none" stroke="currentColor" stroke-width="1.7" />
        <path
          d="M12 2.5v2.5M12 19v2.5M4.2 4.2l1.8 1.8M18 18l1.8 1.8M2.5 12H5M19 12h2.5M4.2 19.8 6 18M18 6l1.8-1.8"
          fill="none"
          stroke="currentColor"
          stroke-width="1.7"
          stroke-linecap="round"
        />
      </svg>
    </button>
  </div>
</template>
