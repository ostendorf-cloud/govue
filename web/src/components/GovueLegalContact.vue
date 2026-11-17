<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { computed } from 'vue'

import { DEFAULT_LOCALE, isLocaleCode } from '../i18n/catalogues'
import { useLanguage } from '../i18n/plugin'
import type { LegalProvider } from '../legal/types'

/**
 * The provider's address or contact lines, shared by the imprint and the
 * controller notice of the privacy policy. Not exported: a site uses the two
 * pages.
 */
const props = defineProps<{
  provider: LegalProvider
  part: 'address' | 'contact'
}>()

const language = useLanguage()
const locale = computed(() =>
  isLocaleCode(language.locale.value) ? language.locale.value : DEFAULT_LOCALE,
)
const country = computed(() => props.provider.country?.[locale.value])
const telephone = computed(() => props.provider.phone.replace(/\s+/g, ''))
</script>

<template>
  <p v-if="part === 'address'">
    {{ provider.name }}<br />
    {{ provider.street }}<br />
    {{ provider.postalCode }} {{ provider.city }}<template v-if="country">, {{ country }}</template>
  </p>
  <p v-else>
    {{ language.t('govue.legal.imprint.phone') }}
    <a class="text-accent-text hover:underline" :href="`tel:${telephone}`">{{ provider.phone }}</a
    ><br />
    {{ language.t('govue.legal.imprint.email') }}
    <a class="text-accent-text hover:underline" :href="`mailto:${provider.email}`">{{
      provider.email
    }}</a>
  </p>
</template>
