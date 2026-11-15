<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { computed, useId } from 'vue'

export type CardAs = 'div' | 'section' | 'article'

/** The level of the card title. Level 1 is the page's own heading. */
export type CardHeadingLevel = 2 | 3 | 4 | 5 | 6

const headingLevels: readonly number[] = [2, 3, 4, 5, 6]

const props = withDefaults(
  defineProps<{
    /** section or article when the card is a part of the page a reader can
     *  jump to, div when it is only a surface. */
    as?: CardAs
    /** The heading above the content. */
    title?: string
    /** The heading level of the title, 2 to 6. Pick the level below the
     *  nearest heading above the card: 2 under the page's h1, 3 when the
     *  card sits in a section headed by an h2. An invalid value renders h2. */
    headingLevel?: CardHeadingLevel
  }>(),
  { as: 'div', title: undefined, headingLevel: 2 },
)

const titleId = useId()

// A value the types would have refused still never becomes an invalid tag.
const headingTag = computed(() =>
  headingLevels.includes(props.headingLevel) ? `h${props.headingLevel}` : 'h2',
)

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
      <component :is="headingTag" v-if="title" :id="titleId" class="text-lg font-semibold">
        {{ title }}
      </component>
      <slot name="header" />
    </div>

    <div class="p-4"><slot /></div>

    <div v-if="$slots.footer" class="flex flex-wrap gap-2 border-t border-border p-4">
      <slot name="footer" />
    </div>
  </component>
</template>
