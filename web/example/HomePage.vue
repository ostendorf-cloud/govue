<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import GovueFooter from '../src/components/GovueFooter.vue'
import GovuePage from '../src/components/GovuePage.vue'
import GovueSkipLink from '../src/components/GovueSkipLink.vue'
import ExampleHeader from './ExampleHeader.vue'

// The example's start page: the solid sticky header and the footer. The header
// carries the compact theme switch, three icons of 44px, about 142px in all,
// which fit a 360px phone next to a brand. The footer links the legal pages in
// the visitor's language.
</script>

<template>
  <GovueSkipLink />
  <ExampleHeader path="/" />
  <GovuePage title="govue example">
    <p class="text-base text-text">Placeholder start page.</p>
  </GovuePage>
  <GovueFooter :legal="['imprint', 'privacy', 'terms']" />
</template>
