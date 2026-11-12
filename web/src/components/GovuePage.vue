<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { computed } from 'vue'

/** reading holds a text column at 60 to 75 characters, shell is for a page
 *  with a sidebar or a wide table. */
export type PageWidth = 'reading' | 'shell'

const props = withDefaults(
  defineProps<{
    width?: PageWidth
    /** The page's one h1. Leave it out when the page brings its own heading. */
    title?: string
    /** One sentence under the heading saying what the page is for. */
    lead?: string
  }>(),
  { width: 'reading', title: undefined, lead: undefined },
)

const widths: Record<PageWidth, string> = {
  reading: 'max-w-reading',
  shell: 'max-w-shell',
}

const measure = computed(() => widths[props.width])
</script>

<template>
  <!-- The skip link's destination, and the one main landmark of the page. -->
  <main id="main" :class="['mx-auto w-full px-4 py-8 sm:px-6 sm:py-10', measure]">
    <div v-if="title || lead" class="mb-6 flex flex-col gap-2">
      <h1 v-if="title" class="text-2xl font-semibold text-text">{{ title }}</h1>
      <p v-if="lead" class="text-lg text-text-muted">{{ lead }}</p>
    </div>

    <div class="flex flex-col gap-6">
      <slot />
    </div>
  </main>
</template>
