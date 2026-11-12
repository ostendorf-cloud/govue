<script setup lang="ts">
import { computed, inject } from 'vue'

import { fieldKey } from './field'

const props = withDefaults(
  defineProps<{
    /** The text kinds only. A checkbox, a select and a textarea are their own
     *  components. */
    type?: 'text' | 'email' | 'url' | 'password' | 'search' | 'tel' | 'number'
    placeholder?: string
    /** Set by the surrounding field; only pass it for a standalone input. */
    invalid?: boolean
    disabled?: boolean
    readonly?: boolean
    autocomplete?: string
    inputmode?: 'text' | 'numeric' | 'decimal' | 'email' | 'url' | 'tel' | 'search'
  }>(),
  { type: 'text', placeholder: undefined, autocomplete: undefined, inputmode: undefined },
)

const model = defineModel<string>({ default: '' })

// A field around the input supplies the id and the description; without one
// the caller is responsible for the label and passes `invalid`/`disabled`
// itself.
const field = inject(fieldKey, undefined)

const invalid = computed(() => props.invalid || Boolean(field?.invalid.value))
const disabled = computed(() => props.disabled || Boolean(field?.disabled.value))

const classes = computed(() => [
  'w-full min-h-11 rounded-md border bg-surface px-3 text-base text-text transition-colors',
  'placeholder:text-text-faint',
  invalid.value ? 'border-danger-strong' : 'border-border-strong hover:border-accent',
  disabled.value ? 'cursor-not-allowed opacity-60' : '',
])
</script>

<template>
  <input
    :id="field?.id"
    v-model="model"
    :type="type"
    :class="classes"
    :placeholder="placeholder"
    :disabled="disabled || undefined"
    :readonly="readonly || undefined"
    :required="field?.required.value || undefined"
    :autocomplete="autocomplete"
    :inputmode="inputmode"
    :aria-describedby="field?.describedBy.value"
    :aria-invalid="invalid ? 'true' : undefined"
  />
</template>
