<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import GovueButton from './GovueButton.vue'
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
      <GovueButton variant="secondary" :disabled="loading" @click="open = false">
        {{ cancelLabel }}
      </GovueButton>
      <GovueButton variant="danger" :loading="loading" @click="confirm">
        {{ confirmLabel }}
      </GovueButton>
    </template>
  </GovueDialog>
</template>
