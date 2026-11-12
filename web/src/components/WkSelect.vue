<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// A native <select> cannot be restyled into this package's token driven
// dropdown panel: the surface, the border and the hover states on the list
// itself are exactly what the platform widget refuses to hand over. A
// hand-rolled listbox would have to reimplement combobox ARIA, roving focus
// and typeahead correctly, which is the kind of accessibility sensitive work
// AGENTS.md says to evaluate a primitives library for first. So this is built
// on Reka UI's Select, styled entirely with this package's own tokens and no
// Reka default theme.
import { computed, inject } from 'vue'
import {
  SelectContent,
  SelectIcon,
  SelectItem,
  SelectItemIndicator,
  SelectItemText,
  SelectPortal,
  SelectRoot,
  SelectTrigger,
  SelectValue,
  SelectViewport,
} from 'reka-ui'

import { fieldKey } from './field'

// SelectRoot renders no DOM element of its own, only Popper's context
// plumbing, so Vue's automatic attrs fallthrough has nowhere to land. The
// trigger is the one real interactive element, so a standalone caller's
// aria-label (or any other passthrough attribute) is bound there by hand.
defineOptions({ inheritAttrs: false })

interface SelectOption {
  value: string
  label: string
  disabled?: boolean
}

const props = withDefaults(
  defineProps<{
    options: SelectOption[]
    /** Shown while nothing is chosen. It is not a label and it cannot be
     *  chosen itself. */
    placeholder?: string
    /** Set by the surrounding field; only pass it for a standalone select. */
    invalid?: boolean
    disabled?: boolean
  }>(),
  { placeholder: undefined },
)

const model = defineModel<string>({ default: '' })

// A field around the select supplies the id and the description; without one
// the caller is responsible for the label, which it then passes as attributes.
const field = inject(fieldKey, undefined)

const invalid = computed(() => props.invalid || Boolean(field?.invalid.value))
const disabled = computed(() => props.disabled || Boolean(field?.disabled.value))

// Reka's own SelectValue reads the chosen option's label off the mounted
// <SelectItem>, but the content only mounts once the dropdown has been
// opened at least once. The label is already in `options`, so it is looked
// up there instead: the trigger then shows the right text from the very
// first render, selected or not.
const selectedLabel = computed(
  () => props.options.find((option) => option.value === model.value)?.label,
)

const triggerClasses = computed(() => [
  'flex w-full min-h-11 items-center justify-between gap-2 rounded-md border bg-surface px-3',
  'text-base text-text transition-colors',
  invalid.value ? 'border-danger-strong' : 'border-border-strong hover:border-accent',
  disabled.value ? 'cursor-not-allowed opacity-60' : '',
])
</script>

<template>
  <SelectRoot v-model="model" :disabled="disabled" :required="field?.required.value">
    <SelectTrigger
      v-bind="$attrs"
      :id="field?.id"
      :class="triggerClasses"
      :aria-describedby="field?.describedBy.value"
      :aria-invalid="invalid ? 'true' : undefined"
    >
      <SelectValue :placeholder="placeholder" class="truncate text-left">
        {{ selectedLabel ?? placeholder }}
      </SelectValue>
      <!-- aria-hidden through SelectIcon: the trigger's own role="combobox"
           and its expanded state already say everything the chevron shows. -->
      <SelectIcon as="span" class="shrink-0 text-text-muted">
        <svg class="size-4" viewBox="0 0 16 16" aria-hidden="true" focusable="false">
          <path
            d="M4 6l4 4 4-4"
            fill="none"
            stroke="currentColor"
            stroke-width="1.5"
            stroke-linecap="round"
            stroke-linejoin="round"
          />
        </svg>
      </SelectIcon>
    </SelectTrigger>

    <SelectPortal>
      <SelectContent
        position="popper"
        :side-offset="4"
        class="z-50 min-w-[var(--reka-select-trigger-width)] overflow-hidden rounded-md border border-border bg-surface shadow-dialog"
      >
        <SelectViewport class="max-h-72 overflow-y-auto p-1">
          <SelectItem
            v-for="option in options"
            :key="option.value"
            :value="option.value"
            :disabled="option.disabled"
            class="flex min-h-11 cursor-pointer items-center justify-between gap-2 rounded-sm px-2 text-base text-text data-disabled:cursor-not-allowed data-disabled:opacity-60 data-highlighted:bg-surface-hover"
          >
            <SelectItemText>{{ option.label }}</SelectItemText>
            <SelectItemIndicator class="text-accent-text">
              <svg class="size-4" viewBox="0 0 16 16" aria-hidden="true" focusable="false">
                <path
                  d="M3 8.5l3 3 7-7"
                  fill="none"
                  stroke="currentColor"
                  stroke-width="1.5"
                  stroke-linecap="round"
                  stroke-linejoin="round"
                />
              </svg>
            </SelectItemIndicator>
          </SelectItem>
        </SelectViewport>
      </SelectContent>
    </SelectPortal>
  </SelectRoot>
</template>
