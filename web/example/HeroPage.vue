<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { computed } from 'vue'

import GovueFooter from '../src/components/GovueFooter.vue'
import GovuePage from '../src/components/GovuePage.vue'
import GovueSkipLink from '../src/components/GovueSkipLink.vue'
import { useLanguage } from '../src/i18n/plugin'
import ExampleHeader from './ExampleHeader.vue'

// The example's overlay page: the header is out of the flow, so the hero starts
// at the top of the window and the header lies over it, transparent until the
// page scrolls. What the host guarantees: the hero behind the header is dark.
// Here its ground is the dark value of the bg role, `[color-scheme:dark]` on the
// section, in the light and the dark page mode alike. A photo needs a scrim.
const { locale } = useLanguage()
const text = computed(() =>
  locale.value === 'de'
    ? { title: 'Overlay Header', lead: 'Der Header liegt transparent über dem Hero.' }
    : { title: 'Overlay header', lead: 'The header lies transparent over the hero.' },
)
</script>

<template>
  <GovueSkipLink />
  <ExampleHeader path="/hero" overlay />
  <!-- The skip link lands on main, which starts after the hero. The hero keeps
       the top padding the header would have taken in the flow. -->
  <section
    class="flex min-h-[70dvh] flex-col justify-center bg-bg px-4 pt-header text-text [color-scheme:dark] sm:px-6"
  >
    <div class="mx-auto w-full max-w-shell">
      <h1 class="text-3xl font-semibold">{{ text.title }}</h1>
      <p class="text-lg text-text-muted">{{ text.lead }}</p>
    </div>
  </section>
  <GovuePage>
    <p class="text-base text-text">Scroll: the header turns solid.</p>
    <p class="min-h-[100dvh] text-base text-text-muted">Placeholder content.</p>
  </GovuePage>
  <GovueFooter :legal="['imprint', 'privacy', 'terms']" />
</template>
