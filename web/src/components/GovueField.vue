<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { computed, provide, useId } from 'vue'

import { fieldKey } from './field'

const props = withDefaults(
  defineProps<{
    /** Always visible. A placeholder inside the control is not a label. */
    label: string
    /** What the value is for, or what shape it has to have. */
    hint?: string
    /** The message under the control. Pass it only once the field was touched. */
    error?: string
    required?: boolean
    disabled?: boolean
  }>(),
  { hint: undefined, error: undefined },
)

const id = useId()
const hintId = `${id}-hint`
const errorId = `${id}-error`

const invalid = computed(() => Boolean(props.error))
const required = computed(() => Boolean(props.required))
const disabled = computed(() => Boolean(props.disabled))

// The control points at both, in reading order: what the field is for, then
// what is currently wrong with it.
const describedBy = computed(() => {
  const ids = [props.hint ? hintId : '', props.error ? errorId : ''].filter(Boolean)
  return ids.length > 0 ? ids.join(' ') : undefined
})

provide(fieldKey, { id, describedBy, invalid, required, disabled })
</script>

<template>
  <div class="flex flex-col gap-1.5">
    <label :for="id" class="text-sm font-medium text-text">
      {{ label }}
      <!-- Spelled out: an asterisk alone is a convention not everybody knows,
           and a screen reader reads it as "star". -->
      <span v-if="required" class="font-normal text-text-muted">(required)</span>
    </label>

    <slot :id="id" :described-by="describedBy" :invalid="invalid" />

    <p v-if="hint" :id="hintId" class="text-sm text-text-muted">{{ hint }}</p>

    <!-- role="alert" announces the error the moment it appears, and it names
         the field, so hearing it out of context still says what to fix. -->
    <p v-if="error" :id="errorId" class="text-sm text-danger-text" role="alert">
      {{ error }}
    </p>
  </div>
</template>
