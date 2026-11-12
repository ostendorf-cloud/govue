<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// The confirm and cancel buttons are plain <button> elements styled with the
// same secondary/danger role-token classes GovueButton uses for those variants,
// not GovueButton itself: GovueButton does not exist on main yet (it ships in the
// still-unmerged forms pull request), and this component must not depend on
// an unmerged component from another pull request. The same reasoning #16
// already applies to the reka-ui dependency itself.
import GovueDialog from './GovueDialog.vue'

withDefaults(
  defineProps<{
    /** The question, as a question: "Delete this entry?" */
    title: string
    /** What happens, and what cannot be undone. */
    message: string
    /** The name of the thing. Confirming is never done from memory. */
    subject: string
    confirmLabel?: string
    cancelLabel?: string
    /** The confirmed action is in flight. */
    loading?: boolean
  }>(),
  { confirmLabel: 'Delete', cancelLabel: 'Cancel' },
)

const open = defineModel<boolean>('open', { default: false })

const emit = defineEmits<{ confirm: [] }>()

/** Closing is the caller's decision: the action may still be running, and it
 *  may fail. */
function confirm(): void {
  emit('confirm')
}
</script>

<template>
  <!-- Not dismissible: a destructive question is answered, not escaped. -->
  <GovueDialog
    v-model:open="open"
    :title="title"
    :description="message"
    :dismissible="false"
    size="sm"
  >
    <p class="rounded-md bg-bg-subtle px-3 py-2 text-base font-semibold text-text">
      {{ subject }}
    </p>

    <template #actions>
      <!-- Cancel comes first, so the keyboard reaches the way out before the
           destructive one. -->
      <button
        type="button"
        class="inline-flex min-h-11 items-center justify-center rounded-md border border-border-strong bg-surface px-4 text-base font-medium text-text transition-colors hover:bg-surface-hover disabled:pointer-events-none disabled:opacity-60"
        :disabled="loading"
        @click="open = false"
      >
        {{ cancelLabel }}
      </button>
      <button
        type="button"
        class="inline-flex min-h-11 items-center justify-center rounded-md bg-danger-strong px-4 text-base font-medium text-danger-on transition-colors hover:bg-danger-strong-hover disabled:pointer-events-none disabled:opacity-60"
        :disabled="loading"
        @click="confirm"
      >
        {{ confirmLabel }}
      </button>
    </template>
  </GovueDialog>
</template>
