<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import GovueFooter from '../src/components/GovueFooter.vue'
import GovuePage from '../src/components/GovuePage.vue'
import GovueSkipLink from '../src/components/GovueSkipLink.vue'

// The example's start page. Its only job is the footer: every page links the
// legal pages in the visitor's language.
</script>

<template>
  <GovueSkipLink />
  <GovuePage title="govue example">
    <p class="text-base text-text">Placeholder start page.</p>
  </GovuePage>
  <GovueFooter :legal="['imprint', 'privacy', 'terms']" />
</template>
