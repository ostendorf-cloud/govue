<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// A hand-rolled <dialog> cannot be verified for the properties that matter
// most here: a real focus trap, an inert background and reliable Escape
// handling. jsdom has no layout engine and does not enforce `inert`, so a
// test against a hand-rolled version could only ever check that the right
// attributes were set, never that the behaviour actually works. Reka UI's
// Dialog primitive implements all three upstream, maintained against real
// browsers, so this wraps that instead of the native element the previous
// version of this component used.
import { watch } from 'vue'
import {
  DialogContent,
  DialogDescription,
  DialogOverlay,
  DialogPortal,
  DialogRoot,
  DialogTitle,
} from 'reka-ui'

const props = withDefaults(
  defineProps<{
    /** The heading, and the dialog's accessible name. */
    title: string
    /** One line under the heading, read as the description. */
    description?: string
    size?: 'sm' | 'md' | 'lg'
    /** False for a dialog that must be answered: Escape and the backdrop then
     *  do not close it. */
    dismissible?: boolean
  }>(),
  { description: undefined, size: 'md', dismissible: true },
)

const open = defineModel<boolean>('open', { default: false })

const emit = defineEmits<{ close: [] }>()

// The dialog closes for several reasons: Escape, a click outside, or the
// caller setting the model itself. All of them end the same way, so "close"
// fires once, here, rather than once per cause.
watch(open, (value, previous) => {
  if (previous && !value) {
    emit('close')
  }
})

/** Escape is a dismiss attempt Reka handles itself; a non-dismissible dialog
 *  only blocks it, it does not act on it. */
function onEscapeKeyDown(event: Event): void {
  if (!props.dismissible) {
    event.preventDefault()
  }
}

/** A pointer down outside the content, which includes the overlay: the same
 *  rule as Escape applies. */
function onPointerDownOutside(event: Event): void {
  if (!props.dismissible) {
    event.preventDefault()
  }
}

const sizes: Record<'sm' | 'md' | 'lg', string> = {
  sm: 'max-w-sm',
  md: 'max-w-md',
  lg: 'max-w-2xl',
}
</script>

<template>
  <DialogRoot v-model:open="open">
    <DialogPortal>
      <DialogOverlay class="fixed inset-0 z-40 bg-overlay" />
      <DialogContent
        :class="[
          'fixed top-1/2 left-1/2 z-50 w-[calc(100%-2rem)] -translate-x-1/2 -translate-y-1/2 rounded-lg',
          'max-h-[85vh] overflow-y-auto border border-border bg-surface p-0 text-text shadow-dialog',
          sizes[size],
        ]"
        @escape-key-down="onEscapeKeyDown"
        @pointer-down-outside="onPointerDownOutside"
      >
        <div class="flex flex-col gap-4 p-5">
          <div class="flex flex-col gap-1">
            <DialogTitle class="text-lg font-semibold">{{ title }}</DialogTitle>
            <DialogDescription v-if="description" class="text-sm text-text-muted">
              {{ description }}
            </DialogDescription>
          </div>

          <div v-if="$slots.default" class="text-base"><slot /></div>

          <!-- The actions sit in reading order and wrap on a narrow screen, so
               the confirming one is never pushed off the edge. -->
          <div v-if="$slots.actions" class="flex flex-wrap justify-end gap-2">
            <slot name="actions" />
          </div>
        </div>
      </DialogContent>
    </DialogPortal>
  </DialogRoot>
</template>
