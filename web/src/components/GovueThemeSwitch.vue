<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { computed, ref } from 'vue'

import { nextMode, useTheme, type ColorMode } from '../composables/useTheme'
import { useGovueText } from '../i18n/plugin'
import ThemeModeIcon from './ThemeModeIcon.vue'

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
// ("Light mode"), since an icon says nothing to a screen reader. The group
// name stays static: the checked state already announces the current mode. The
// three targets
// take about 142px: 3 x 44px, two 4px gaps and the 1px border on both sides
// (computed from the classes, jsdom has no layout), against the 328px of a
// 360px phone header. The icons use currentColor, so the role tokens colour
// them, and the transition is dropped by the global reduced motion rule.
//
// With cycle set, the switch is one native button of 44 by 44px with the icon
// of the current mode. A click, Enter or Space moves to the next mode through
// useTheme's own cycleMode: system, light, dark, then system again. It is no
// radio group: there is one control and one action. Its accessible name says
// the current mode and the next one, built from the same names as the compact
// radios, so a visitor knows what a press does before pressing. cycle wins
// over compact when both are set.
//
// It shares the state of useTheme, and the first useTheme call decides the
// default theme and the storage key. An app that sets either calls
// useTheme({ ... }) in its setup before this component renders.
const props = withDefaults(
  defineProps<{
    /** The accessible name of the group, and the first part of the cycle button's name. */
    label?: string
    /** The name of an option: its text, or in the compact and cycle variants its accessible name. */
    systemLabel?: string
    lightLabel?: string
    darkLabel?: string
    /** One icon per mode instead of a text button. Off by default. */
    compact?: boolean
    /**
     * One button that steps to the next mode, with the icon of the current
     * one. Wins over compact. Off by default.
     */
    cycle?: boolean
  }>(),
  {
    label: undefined,
    systemLabel: undefined,
    lightLabel: undefined,
    darkLabel: undefined,
    compact: false,
    cycle: false,
  },
)

const { mode, setMode, cycleMode } = useTheme()
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

/** The name of a mode in the icon variants, a prop or the catalogue. */
function modeName(value: ColorMode): string {
  switch (value) {
    case 'light':
      return props.lightLabel ?? t('govue.theme.compact.light')
    case 'dark':
      return props.darkLabel ?? t('govue.theme.compact.dark')
    default:
      return props.systemLabel ?? t('govue.theme.compact.system')
  }
}

const cycleName = computed(() =>
  t('govue.theme.cycle', {
    label: props.label ?? t('govue.theme.label'),
    current: modeName(mode.value),
    next: modeName(nextMode(mode.value)),
  }),
)

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
  <button
    v-if="cycle"
    type="button"
    :aria-label="cycleName"
    :data-mode="mode"
    class="inline-flex size-11 shrink-0 items-center justify-center rounded-md border border-border-strong bg-surface text-text transition-colors hover:bg-surface-hover active:bg-bg-subtle"
    @click="cycleMode()"
  >
    <ThemeModeIcon :mode="mode" />
  </button>
  <div
    v-else
    role="radiogroup"
    :aria-label="label ?? t('govue.theme.label')"
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
      <ThemeModeIcon v-else :mode="option.value" />
    </button>
  </div>
</template>
