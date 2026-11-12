<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { computed } from 'vue'

/** How much of the container the placeholder takes, so it has the shape of
 *  the content that replaces it. */
export type SkeletonWidth = 'full' | 'wide' | 'half' | 'narrow'

const props = withDefaults(
  defineProps<{
    /** How many lines of text are coming. Ignored for a block. */
    lines?: number
    width?: SkeletonWidth
    /** A block instead of lines: an avatar, a thumbnail, a map. */
    rounded?: boolean
  }>(),
  { lines: 3, width: 'full' },
)

const widths: Record<SkeletonWidth, string> = {
  full: 'w-full',
  wide: 'w-3/4',
  half: 'w-1/2',
  narrow: 'w-1/3',
}

const width = computed(() => widths[props.width])

const count = computed(() => Math.max(1, Math.trunc(props.lines)))

/** The last line of a paragraph stops short, which is what makes the block
 *  read as text rather than as a bar chart. */
const lastWidth = computed(() => (props.width === 'narrow' ? 'w-1/4' : 'w-2/3'))
</script>

<template>
  <!-- Hidden from assistive technology: the caller owns the live region that
       says something is loading. The pulse stops under prefers-reduced-motion,
       which the base stylesheet handles for every animation. -->
  <div aria-hidden="true">
    <div v-if="rounded" :class="['aspect-square animate-pulse rounded-lg bg-bg-subtle', width]" />

    <div v-else class="flex flex-col gap-2">
      <div
        v-for="line in count"
        :key="line"
        :class="['h-4 animate-pulse rounded-sm bg-bg-subtle', line === count ? lastWidth : width]"
      />
    </div>
  </div>
</template>
