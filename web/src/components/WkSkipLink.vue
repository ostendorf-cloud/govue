<script setup lang="ts">
withDefaults(
  defineProps<{
    /** The main landmark of the page. WkPage carries this id. */
    target?: string
  }>(),
  { target: '#main' },
)
</script>

<template>
  <!-- The first focusable thing on the page: a keyboard visitor must not walk
       the whole header to reach the content. It is off screen until it has
       focus, and then it is the one element that styles its own focused look.
       This is not a focus ring: the global :focus-visible outline in
       base.css still applies on top, this only controls visibility. -->
  <a
    :href="target"
    class="sr-only focus:not-sr-only focus:absolute focus:top-2 focus:left-2 focus:z-50 focus:inline-flex focus:min-h-11 focus:items-center focus:rounded-md focus:border focus:border-border-strong focus:bg-surface focus:px-4 focus:text-base focus:font-medium focus:text-text focus:shadow-dialog"
  >
    <slot>Skip to content</slot>
  </a>
</template>
