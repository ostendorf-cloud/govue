<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { computed, inject, useId } from 'vue'

import { fieldKey } from './field'

// The character count sits next to the control, so the wrapper div is not
// what a caller addresses: a label or a describedby belongs on the textarea
// itself.
defineOptions({ inheritAttrs: false })

const props = withDefaults(
  defineProps<{
    /** The visible height. Four lines is a comment, not a one liner. */
    rows?: number
    /** Caps the value and shows how much room is left. */
    maxlength?: number
    placeholder?: string
    /** Set by the surrounding field; only pass it for a standalone textarea. */
    invalid?: boolean
    disabled?: boolean
    readonly?: boolean
  }>(),
  { rows: 4, maxlength: undefined, placeholder: undefined },
)

const model = defineModel<string>({ default: '' })

const field = inject(fieldKey, undefined)

const countId = useId()

const invalid = computed(() => props.invalid || Boolean(field?.invalid.value))
const disabled = computed(() => props.disabled || Boolean(field?.disabled.value))

const remaining = computed(() =>
  props.maxlength === undefined ? undefined : props.maxlength - model.value.length,
)

// The count joins the description rather than its own live region:
// announcing it on every keystroke would talk over the typing.
const describedBy = computed(() => {
  const ids = [field?.describedBy.value, props.maxlength === undefined ? undefined : countId]
  const present = ids.filter((id): id is string => Boolean(id))
  return present.length > 0 ? present.join(' ') : undefined
})

const classes = computed(() => [
  'w-full min-h-11 rounded-md border bg-surface px-3 py-2 text-base text-text transition-colors',
  'placeholder:text-text-faint',
  invalid.value ? 'border-danger-strong' : 'border-border-strong hover:border-accent',
  disabled.value ? 'cursor-not-allowed opacity-60' : '',
])
</script>

<template>
  <div class="flex flex-col gap-1">
    <textarea
      :id="field?.id"
      v-bind="$attrs"
      v-model="model"
      :class="classes"
      :rows="rows"
      :maxlength="maxlength"
      :placeholder="placeholder"
      :disabled="disabled || undefined"
      :readonly="readonly || undefined"
      :required="field?.required.value || undefined"
      :aria-describedby="describedBy"
      :aria-invalid="invalid ? 'true' : undefined"
    />

    <p v-if="remaining !== undefined" :id="countId" class="text-sm text-text-muted">
      {{ remaining }} characters left
    </p>
  </div>
</template>
