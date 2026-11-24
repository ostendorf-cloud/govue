<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// A harness page for the header width spec: GovueHeader with the narrow
// parts a site uses below 1024px. A brand of a usual length, a navigation
// with a call to action (behind the menu button below 640px), the cycle theme
// button and the link to the other language. The spec measures the real
// layout at phone and tablet widths, which jsdom cannot.
import GovueHeader from '../../src/components/GovueHeader.vue'
import GovueLanguageSwitch from '../../src/components/GovueLanguageSwitch.vue'
import GovueNav, { type NavItem } from '../../src/components/GovueNav.vue'
import GovueThemeSwitch from '../../src/components/GovueThemeSwitch.vue'

const items: NavItem[] = [
  { href: '/en/projects', label: 'Projects' },
  { href: '/en/contact', label: 'Contact', variant: 'cta' },
]

function linkFor(code: string): string {
  return `/${code}/`
}
</script>

<template>
  <GovueHeader nav-placement="end">
    <template #brand>
      <a href="/en/" data-part="brand">Example Studio</a>
    </template>
    <GovueNav :items="items" />
    <template #theme>
      <GovueThemeSwitch cycle />
    </template>
    <template #actions>
      <GovueLanguageSwitch :link-for="linkFor" others-only />
    </template>
  </GovueHeader>
  <main class="p-4">
    <p>Page content under the header.</p>
  </main>
</template>
