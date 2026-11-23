<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import GovueFooter from '../src/components/GovueFooter.vue'
import GovueHeader from '../src/components/GovueHeader.vue'
import GovuePage from '../src/components/GovuePage.vue'
import GovueSkipLink from '../src/components/GovueSkipLink.vue'
import GovueThemeSwitch from '../src/components/GovueThemeSwitch.vue'

// The example's start page. It shows the footer, which links the legal pages
// in the visitor's language, and the compact theme switch in the header: three
// icons of 44px, about 142px in all, which fit a 360px phone next to a brand.
</script>

<template>
  <GovueSkipLink />
  <GovueHeader>
    <template #brand>govue example</template>
    <template #theme>
      <GovueThemeSwitch compact />
    </template>
  </GovueHeader>
  <GovuePage title="govue example">
    <p class="text-base text-text">Placeholder start page.</p>
  </GovuePage>
  <GovueFooter :legal="['imprint', 'privacy', 'terms']" />
</template>
