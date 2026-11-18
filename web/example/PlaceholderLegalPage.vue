<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { computed } from 'vue'

import GovueFooter from '../src/components/GovueFooter.vue'
import GovueLegalPage from '../src/components/GovueLegalPage.vue'
import type { LegalPage } from '../src/i18n/legal'
import { useLanguage } from '../src/i18n/plugin'

// The example's legal page. The text is a placeholder that says so. A real
// site writes its own text here, reviewed by its owner. govue ships none.

const props = defineProps<{ page: LegalPage }>()

const { locale } = useLanguage()

const placeholder = computed(() =>
  locale.value === 'de'
    ? {
        heading: 'Platzhalter',
        text: 'Dies ist ein Platzhalter und kein rechtlicher Text. Eine Website schreibt hier ihren eigenen Text.',
      }
    : {
        heading: 'Placeholder',
        text: 'This is a placeholder and no legal text. A site writes its own text here.',
      },
)
</script>

<template>
  <GovueLegalPage :page="props.page">
    <section data-placeholder="legal">
      <h2 class="text-xl font-semibold text-text">{{ placeholder.heading }}</h2>
      <p class="text-base text-text">{{ placeholder.text }}</p>
    </section>
  </GovueLegalPage>
  <GovueFooter :legal="['imprint', 'privacy', 'terms']" />
</template>
