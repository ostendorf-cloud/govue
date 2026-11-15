<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { useGovueText } from '../i18n/plugin'
import GovueButton from './GovueButton.vue'

withDefaults(
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
  }>(),
  { title: undefined, message: undefined, requestId: undefined, retryLabel: undefined },
)

const { t } = useGovueText()

const emit = defineEmits<{ retry: [] }>()
</script>

<template>
  <!-- A failure the visitor did not ask for, so it is announced when it
       appears. -->
  <div
    role="alert"
    class="flex flex-col items-start gap-3 rounded-lg border border-border bg-danger-subtle px-5 py-6 text-text"
  >
    <h2 class="text-lg font-semibold text-danger-text">{{ title ?? t('govue.error.title') }}</h2>
    <p class="text-base">{{ message ?? t('govue.error.message') }}</p>

    <p v-if="requestId" class="text-sm text-text-muted">
      {{ t('govue.error.requestId') }}: <code class="font-mono">{{ requestId }}</code>
    </p>

    <GovueButton variant="secondary" :loading="loading" @click="emit('retry')">
      {{ retryLabel ?? t('govue.error.retry') }}
    </GovueButton>
  </div>
</template>
