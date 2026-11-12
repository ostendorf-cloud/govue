<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { computed, useId } from 'vue'

export type CardAs = 'div' | 'section' | 'article'

const props = withDefaults(
  defineProps<{
    /** section or article when the card is a part of the page a reader can
     *  jump to, div when it is only a surface. */
    as?: CardAs
    /** The heading above the content. */
    title?: string
  }>(),
  { as: 'div', title: undefined },
)

const titleId = useId()

// A div is not a region, so naming it would say nothing. The two sectioning
// elements are what a screen reader can list.
const labelledBy = computed(() => (props.title && props.as !== 'div' ? titleId : undefined))
</script>

<template>
  <component
    :is="as"
    class="flex flex-col rounded-lg border border-border bg-surface text-text shadow-card"
    :aria-labelledby="labelledBy"
  >
    <div v-if="title || $slots.header" class="flex flex-col gap-1 border-b border-border p-4">
      <h3 v-if="title" :id="titleId" class="text-lg font-semibold">{{ title }}</h3>
      <slot name="header" />
    </div>

    <div class="p-4"><slot /></div>

    <div v-if="$slots.footer" class="flex flex-wrap gap-2 border-t border-border p-4">
      <slot name="footer" />
    </div>
  </component>
</template>
