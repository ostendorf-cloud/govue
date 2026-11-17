<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { computed } from 'vue'

import { DEFAULT_LOCALE, isLocaleCode } from '../i18n/catalogues'
import { useLanguage } from '../i18n/plugin'
import { useLegalProvider } from '../legal/provider'
import type { LegalProvider } from '../legal/types'
import GovueLegalContact from './GovueLegalContact.vue'
import GovuePage from './GovuePage.vue'

const props = defineProps<{
  /** Overrides the provider the site passed to createGovueLegal. */
  provider?: LegalProvider
  /** Where the German page lives. Shown on the English page, next to the binding notice. */
  germanHref?: string
}>()

const language = useLanguage()
const locale = computed(() =>
  isLocaleCode(language.locale.value) ? language.locale.value : DEFAULT_LOCALE,
)
const data = computed(() => useLegalProvider(props.provider))
const t = language.t
</script>

<template>
  <GovuePage width="reading" :title="t('govue.legal.imprint.title')">
    <div data-govue-legal="imprint" :lang="locale" class="flex flex-col gap-4 text-text">
      <p v-if="locale !== 'de'" class="text-text-muted">
        {{ t('govue.legal.bindingNotice') }}
        <a v-if="germanHref" class="text-accent-text hover:underline" :href="germanHref">{{
          t('govue.legal.viewGerman')
        }}</a>
      </p>

      <GovueLegalContact :provider="data" part="address" />

      <h2 class="text-xl font-semibold">{{ t('govue.legal.imprint.contact') }}</h2>
      <GovueLegalContact :provider="data" part="contact" />

      <h2 class="text-xl font-semibold">{{ t('govue.legal.imprint.vatHeading') }}</h2>
      <p>{{ t('govue.legal.imprint.vatText', { vatId: data.vatId }) }}</p>

      <h2 class="text-xl font-semibold">{{ t('govue.legal.imprint.disputeHeading') }}</h2>
      <p>{{ data.disputeResolution[locale] }}</p>
    </div>
  </GovuePage>
</template>
