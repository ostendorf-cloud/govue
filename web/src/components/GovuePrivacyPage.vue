<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { computed, useSlots } from 'vue'

import { DEFAULT_LOCALE, isLocaleCode } from '../i18n/catalogues'
import { useLanguage } from '../i18n/plugin'
import {
  controllerDefinition,
  generalAfterControllerSections,
  generalBeforeControllerSections,
  glanceSections,
  hostingSections,
} from '../legal/privacyText'
import { useLegalProvider } from '../legal/provider'
import type { LegalProvider, LegalSection } from '../legal/types'
import GovueLegalContact from './GovueLegalContact.vue'
import GovueLegalSections from './GovueLegalSections.vue'
import GovuePage from './GovuePage.vue'

const props = withDefaults(
  defineProps<{
    /** Overrides the provider the site passed to createGovueLegal. */
    provider?: LegalProvider
    /** Where the German page lives. Shown on the English page, next to the binding notice. */
    germanHref?: string
    /**
     * The sections of part 4, data collection on this website: the optional
     * blocks and the site's own. Part 4 shows when this list or the default
     * slot has content.
     */
    sections?: LegalSection[]
  }>(),
  { provider: undefined, germanHref: undefined, sections: () => [] },
)

const slots = useSlots()
const language = useLanguage()
const locale = computed(() =>
  isLocaleCode(language.locale.value) ? language.locale.value : DEFAULT_LOCALE,
)
const data = computed(() => useLegalProvider(props.provider))
const hasCollection = computed(() => props.sections.length > 0 || Boolean(slots.default))
const t = language.t
</script>

<template>
  <GovuePage width="reading" :title="t('govue.legal.privacy.title')">
    <div data-govue-legal="privacy" :lang="locale" class="flex flex-col gap-4 text-text">
      <p v-if="locale !== 'de'" class="text-text-muted">
        {{ t('govue.legal.bindingNotice') }}
        <a v-if="germanHref" class="text-accent-text hover:underline" :href="germanHref">{{
          t('govue.legal.viewGerman')
        }}</a>
      </p>

      <!-- A short summary of what the site does and does not do, written by the site. -->
      <p v-if="$slots.intro" class="text-text-muted"><slot name="intro" /></p>

      <h2 class="text-xl font-semibold">1. {{ t('govue.legal.privacy.glance') }}</h2>
      <GovueLegalSections :sections="glanceSections" />
      <slot name="glance" />

      <h2 class="text-xl font-semibold">2. {{ t('govue.legal.privacy.hosting') }}</h2>
      <GovueLegalSections :sections="hostingSections" />

      <h2 class="text-xl font-semibold">3. {{ t('govue.legal.privacy.general') }}</h2>
      <GovueLegalSections :sections="generalBeforeControllerSections" />

      <section id="responsible" class="flex flex-col gap-4">
        <h3 class="text-lg font-semibold">{{ t('govue.legal.privacy.controller') }}</h3>
        <p>{{ t('govue.legal.privacy.controllerIntro') }}</p>
        <GovueLegalContact :provider="data" part="address" />
        <GovueLegalContact :provider="data" part="contact" />
        <p>{{ controllerDefinition[locale] }}</p>
      </section>

      <GovueLegalSections :sections="generalAfterControllerSections" />

      <template v-if="hasCollection">
        <h2 class="text-xl font-semibold">4. {{ t('govue.legal.privacy.collection') }}</h2>
        <GovueLegalSections :sections="sections" />
        <slot />
      </template>

      <!-- Further numbered parts of the site, such as social media, with their own h2. -->
      <slot name="extra" />
    </div>
  </GovuePage>
</template>
