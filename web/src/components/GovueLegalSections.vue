<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { computed } from 'vue'

import { DEFAULT_LOCALE, isLocaleCode } from '../i18n/catalogues'
import { useLanguage } from '../i18n/plugin'
import type { LegalSection } from '../legal/types'

const props = defineProps<{
  /** The sections to render, in order. A block from govue or a site's own. */
  sections: LegalSection[]
}>()

const language = useLanguage()
const locale = computed(() =>
  isLocaleCode(language.locale.value) ? language.locale.value : DEFAULT_LOCALE,
)
</script>

<template>
  <section v-for="item in props.sections" :id="item.id" :key="item.id" class="flex flex-col gap-4">
    <h3 v-if="item.level === 3" class="text-lg font-semibold">{{ item.heading[locale] }}</h3>
    <h4 v-else class="font-semibold">{{ item.heading[locale] }}</h4>

    <template v-for="(block, index) in item.blocks" :key="index">
      <p v-if="block.kind === 'p'" :class="{ uppercase: block.upper }">{{ block.text[locale] }}</p>
      <ul v-else class="list-disc pl-6">
        <li v-for="(entry, entryIndex) in block.items" :key="entryIndex">{{ entry[locale] }}</li>
      </ul>
    </template>
  </section>
</template>
