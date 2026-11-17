<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { computed } from 'vue'

import { useGovueText } from '../i18n/plugin'
import { type HeadingLevel, headingTag as tagFor } from './headingLevel'

const props = withDefaults(
  defineProps<{
    /** What this place is, not "No data". Defaults to a generic line. */
    title?: string
    /** Why it is empty and what will fill it. Defaults to a generic line. */
    description?: string
    /** The heading level of the title, 2 to 6. Pick the level below the
     *  nearest heading above this state: 2 under the page's h1, 3 inside a
     *  section headed by an h2. An invalid value renders h2. */
    headingLevel?: HeadingLevel
  }>(),
  { title: undefined, description: undefined, headingLevel: 2 },
)

const { t } = useGovueText()

const headingTag = computed(() => tagFor(props.headingLevel))
</script>

<template>
  <!-- Centred in whatever holds it, never stretched over the whole page: an
       empty table is a part of a page, not the page. -->
  <div
    class="mx-auto flex max-w-reading flex-col items-center gap-3 rounded-lg border border-border bg-surface px-6 py-10 text-center shadow-card"
  >
    <div v-if="$slots.icon" class="text-text-faint">
      <slot name="icon" />
    </div>

    <component :is="headingTag" class="text-lg font-semibold text-text">
      {{ title ?? t('govue.empty.title') }}
    </component>
    <p class="text-base text-text-muted">
      {{ description ?? t('govue.empty.description') }}
    </p>

    <div v-if="$slots.action" class="flex flex-wrap justify-center gap-2 pt-1">
      <slot name="action" />
    </div>
  </div>
</template>
