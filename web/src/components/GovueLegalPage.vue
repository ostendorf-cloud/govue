<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { computed, inject } from 'vue'

import { isLocaleCode, type LocaleCode } from '../i18n/catalogues'
import { legalPath, type LegalPage } from '../i18n/legal'
import { languageKey, useGovueText } from '../i18n/plugin'
import GovueLanguageSwitch from './GovueLanguageSwitch.vue'
import GovuePage from './GovuePage.vue'
import GovueSkipLink from './GovueSkipLink.vue'

// The shell of one legal page: skip link, back link, language switch and the
// one h1. The text is the site's own and comes through the default slot,
// with its headings starting at h2. govue ships no legal text.

const props = withDefaults(
  defineProps<{
    page: LegalPage
    /** The h1. Defaults to the catalogue's title of the page. */
    title?: string
    /** Where the back link goes. Defaults to the start page of the language. */
    backHref?: string
    /** The back link text. Defaults to the catalogue's. */
    backLabel?: string
    /** Render the skip link. Leave it off when the site's layout has one. */
    skipLink?: boolean
    /** Show the language switch. It needs the language layer. */
    languageSwitch?: boolean
    /**
     * Goes to the page of another language. Defaults to a full page load,
     * since every language is its own prerendered page. A site with a client
     * router passes its push.
     */
    navigate?: (path: string) => void
  }>(),
  {
    title: undefined,
    backHref: undefined,
    backLabel: undefined,
    skipLink: true,
    languageSwitch: true,
    navigate: undefined,
  },
)

const language = inject(languageKey, null)
const { t } = useGovueText()

// Without the language layer the texts are English, so the links are too.
const locale = computed<LocaleCode>(() => {
  const code = language?.locale.value
  return isLocaleCode(code) ? code : 'en'
})

const heading = computed(() => props.title ?? t(`govue.legal.title.${props.page}`))
const back = computed(() => props.backHref ?? `/${locale.value}/`)

// The page of each language is its own URL. Switching the language goes to
// the page in that language, built from the fixed list, never from the
// address bar.
function onLanguageChange(code: string): void {
  if (!isLocaleCode(code)) return
  const path = legalPath(props.page, code)
  if (props.navigate) {
    props.navigate(path)
  } else if (typeof window !== 'undefined') {
    window.location.assign(path)
  }
}
</script>

<template>
  <GovueSkipLink v-if="skipLink" />
  <GovuePage>
    <div class="flex flex-wrap items-center justify-between gap-4">
      <a
        :href="back"
        class="inline-flex min-h-11 items-center text-sm text-accent-text transition-colors hover:underline"
      >
        {{ backLabel ?? t('govue.legal.back') }}
      </a>
      <GovueLanguageSwitch v-if="languageSwitch && language" @change="onLanguageChange" />
    </div>
    <h1 class="text-2xl font-semibold text-text">{{ heading }}</h1>
    <slot />
  </GovuePage>
</template>
