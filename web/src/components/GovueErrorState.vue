<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { computed } from 'vue'

import { useGovueText } from '../i18n/plugin'
import GovueButton from './GovueButton.vue'
import { type HeadingLevel, headingTag as tagFor } from './headingLevel'

const props = withDefaults(
  defineProps<{
    /** What failed, in the visitor's terms. Defaults to a generic line. */
    title?: string
    /** What it means and what to do next. Defaults to a generic line. */
    message?: string
    /** The id the server logged this under. Support can find the request with
     *  it, so it is shown rather than hidden in a console. */
    requestId?: string
    retryLabel?: string
    /** The retry is in flight. */
    loading?: boolean
    /** The heading level of the title, 2 to 6. Pick the level below the
     *  nearest heading above this state: 2 under the page's h1, 3 inside a
     *  section headed by an h2. An invalid value renders h2. */
    headingLevel?: HeadingLevel
  }>(),
  {
    title: undefined,
    message: undefined,
    requestId: undefined,
    retryLabel: undefined,
    headingLevel: 2,
  },
)

const { t } = useGovueText()

const headingTag = computed(() => tagFor(props.headingLevel))

const emit = defineEmits<{ retry: [] }>()
</script>

<template>
  <!-- A failure the visitor did not ask for, so it is announced when it
       appears. -->
  <div
    role="alert"
    class="flex flex-col items-start gap-3 rounded-lg border border-border bg-danger-subtle px-5 py-6 text-text"
  >
    <component :is="headingTag" class="text-lg font-semibold text-danger-text">
      {{ title ?? t('govue.error.title') }}
    </component>
    <p class="text-base">{{ message ?? t('govue.error.message') }}</p>

    <p v-if="requestId" class="text-sm text-text-muted">
      {{ t('govue.error.requestId') }}: <code class="font-mono">{{ requestId }}</code>
    </p>

    <GovueButton variant="secondary" :loading="loading" @click="emit('retry')">
      {{ retryLabel ?? t('govue.error.retry') }}
    </GovueButton>
  </div>
</template>
