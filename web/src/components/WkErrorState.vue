<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// The retry button is a plain <button> styled with the same secondary role-
// token classes WkButton uses for that variant, not WkButton itself: WkButton
// does not exist on main yet (it ships in the still-unmerged forms pull
// request), and this component must not depend on an unmerged component from
// another pull request. The same reasoning #16 already applies to the
// reka-ui dependency itself.
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

    <button
      type="button"
      class="inline-flex min-h-11 items-center justify-center rounded-md border border-border-strong bg-surface px-4 text-base font-medium text-text transition-colors hover:bg-surface-hover disabled:pointer-events-none disabled:opacity-60"
      :disabled="loading"
      @click="emit('retry')"
    >
      {{ retryLabel }}
    </button>
  </div>
</template>
