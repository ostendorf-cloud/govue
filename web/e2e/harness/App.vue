<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// A harness page for the Playwright suite: real page content around
// WkDialog and WkConfirmDialog, the real components from ../../src, so the
// three properties jsdom cannot verify - the focus trap, the inert
// background, and a real Escape key press - run against an actual browser
// layout and input pipeline instead of a synthetic DOM. No Tailwind here:
// only real layout, focus and hit-testing matter, and the components render
// fine without their token classes resolving to anything.
import { ref } from 'vue'

import WkConfirmDialog from '../../src/components/WkConfirmDialog.vue'
import WkDialog from '../../src/components/WkDialog.vue'

// Read once on mount, not reactively: a test navigates with the query
// string already set and never changes it afterwards.
const dismissible = new URLSearchParams(window.location.search).get('dismissible') !== 'false'

const beforeCount = ref(0)
const afterCount = ref(0)
const dialogOpen = ref(false)
const confirmOpen = ref(false)
</script>

<template>
  <div>
    <button id="page-before" type="button" @click="beforeCount++">Before trigger</button>
    <span id="page-before-count">{{ beforeCount }}</span>

    <button id="open-dialog" type="button" @click="dialogOpen = true">Open dialog</button>
    <button id="open-confirm" type="button" @click="confirmOpen = true">Open confirm dialog</button>

    <button id="page-after" type="button" @click="afterCount++">After trigger</button>
    <span id="page-after-count">{{ afterCount }}</span>

    <WkDialog v-model:open="dialogOpen" title="Harness dialog" :dismissible="dismissible">
      <a id="dialog-link" href="#dialog-link">A link inside the dialog</a>
      <template #actions>
        <button id="dialog-action" type="button">Dialog action</button>
      </template>
    </WkDialog>

    <WkConfirmDialog
      v-model:open="confirmOpen"
      title="Delete the harness item?"
      message="This cannot be undone."
      subject="Harness item"
    />
  </div>
</template>
