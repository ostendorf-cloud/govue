<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { computed, useAttrs, useId } from 'vue'

// A native checkbox already gives correct keyboard handling, ARIA and
// platform rendering for free, so this hand-rolls the markup instead of
// reaching for Reka UI: there is nothing a primitives library would add here.
// The hint sits next to the control, so the wrapper is not what a caller
// addresses: an attribute belongs on the checkbox itself.
defineOptions({ inheritAttrs: false })

const props = withDefaults(
  defineProps<{
    /** The checkbox labels itself, so it is used standalone, outside a field. */
    label: string
    /** One line under the label, for what the choice means. */
    hint?: string
    disabled?: boolean
  }>(),
  { hint: undefined },
)

const model = defineModel<boolean>({ default: false })

const id = useId()
const hintId = `${id}-hint`

// A caller's own aria-describedby, such as the id of an error, and the hint
// both describe the checkbox. The attribute is bound after $attrs, so it has
// to carry both or it would replace the caller's.
const attrs = useAttrs()
const describedBy = computed(() => {
  const given = attrs['aria-describedby']
  const ids = [typeof given === 'string' ? given : undefined, props.hint ? hintId : undefined]
  const present = ids.filter((value): value is string => Boolean(value))
  return present.length > 0 ? present.join(' ') : undefined
})
</script>

<template>
  <div class="flex flex-col">
    <!-- The whole row is the label, so the text is part of the hit area and
         the row itself reaches the 44px touch target. -->
    <label
      :for="id"
      :class="[
        'flex min-h-11 items-center gap-3 rounded-md px-2 text-base text-text transition-colors',
        disabled
          ? 'cursor-not-allowed opacity-60'
          : 'cursor-pointer hover:bg-surface-hover active:bg-bg-subtle',
      ]"
    >
      <!-- The native control keeps the platform's checked, indeterminate and
           high contrast rendering. -->
      <input
        :id="id"
        v-bind="$attrs"
        v-model="model"
        type="checkbox"
        class="size-5 shrink-0"
        :disabled="disabled || undefined"
        :aria-describedby="describedBy"
      />
      <!-- The slot is for a label with markup in it, such as a link. The label
           prop stays the plain text and the fallback. -->
      <span
        ><slot>{{ label }}</slot></span
      >
    </label>

    <p v-if="hint" :id="hintId" class="pl-10 text-sm text-text-muted">{{ hint }}</p>
  </div>
</template>
