<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import GovueButton from './GovueButton.vue'

withDefaults(
  defineProps<{
    /** What failed, in the visitor's terms. */
    title: string
    /** What it means and what to do next. */
    message: string
    /** The id the server logged this under. Support can find the request with
     *  it, so it is shown rather than hidden in a console. */
    requestId?: string
    retryLabel?: string
    /** The retry is in flight. */
    loading?: boolean
  }>(),
  { requestId: undefined, retryLabel: 'Try again' },
)

const emit = defineEmits<{ retry: [] }>()
</script>

<template>
  <!-- A failure the visitor did not ask for, so it is announced when it
       appears. -->
  <div
    role="alert"
    class="flex flex-col items-start gap-3 rounded-lg border border-border bg-danger-subtle px-5 py-6 text-text"
  >
    <h2 class="text-lg font-semibold text-danger-text">{{ title }}</h2>
    <p class="text-base">{{ message }}</p>

    <p v-if="requestId" class="text-sm text-text-muted">
      Request id: <code class="font-mono">{{ requestId }}</code>
    </p>

    <GovueButton variant="secondary" :loading="loading" @click="emit('retry')">
      {{ retryLabel }}
    </GovueButton>
  </div>
</template>
