<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import type { ColorMode } from '../composables/useTheme'

// The icon of one colour mode, shared by the compact radios and the cycle
// button of GovueThemeSwitch: a device for system, a sun for light, a moon for
// dark. Inline SVG with currentColor, so the role tokens colour it, and
// aria-hidden, since the control around it carries the name. Internal to
// GovueThemeSwitch, not exported. No comment in the template: it would render
// into the markup the switch tests pin.
defineProps<{ mode: ColorMode }>()
</script>

<template>
  <svg
    v-if="mode === 'system'"
    class="size-5 shrink-0"
    viewBox="0 0 24 24"
    aria-hidden="true"
    focusable="false"
  >
    <rect
      x="3"
      y="4"
      width="18"
      height="12"
      rx="2"
      fill="none"
      stroke="currentColor"
      stroke-width="1.7"
    />
    <path
      d="M8 20h8M12 16v4"
      fill="none"
      stroke="currentColor"
      stroke-width="1.7"
      stroke-linecap="round"
    />
  </svg>
  <svg
    v-else-if="mode === 'dark'"
    class="size-5 shrink-0"
    viewBox="0 0 24 24"
    aria-hidden="true"
    focusable="false"
  >
    <path
      d="M20 14.5A8.5 8.5 0 0 1 9.5 4a8.5 8.5 0 1 0 10.5 10.5Z"
      fill="none"
      stroke="currentColor"
      stroke-width="1.7"
      stroke-linecap="round"
      stroke-linejoin="round"
    />
  </svg>
  <svg v-else class="size-5 shrink-0" viewBox="0 0 24 24" aria-hidden="true" focusable="false">
    <circle cx="12" cy="12" r="4.5" fill="none" stroke="currentColor" stroke-width="1.7" />
    <path
      d="M12 2.5v2.5M12 19v2.5M4.2 4.2l1.8 1.8M18 18l1.8 1.8M2.5 12H5M19 12h2.5M4.2 19.8 6 18M18 6l1.8-1.8"
      fill="none"
      stroke="currentColor"
      stroke-width="1.7"
      stroke-linecap="round"
    />
  </svg>
</template>
