<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { computed } from 'vue'

import GovueHeader from '../src/components/GovueHeader.vue'
import GovueLanguageSwitch from '../src/components/GovueLanguageSwitch.vue'
import GovueNav, { type NavItem } from '../src/components/GovueNav.vue'
import GovueThemeSwitch from '../src/components/GovueThemeSwitch.vue'
import { useLanguage } from '../src/i18n/plugin'
import { localizedPath } from '../src/i18n/routes'

// The header of the example site, and the reference for the header API:
//
// - overlay: the page content starts under the header, which is transparent
//   until the page scrolls (the hero page). Without it the header is the
//   sticky solid bar (the start and the contact page).
// - navPlacement: the navigation sits at the end of the row, next to the
//   actions.
// - a NavItem with variant 'cta': the contact link.
// - the narrow setup below 1024px: the cycle theme button and only the link to
//   the other language, so the row stays one row on a phone. From 1024px the
//   compact theme switch with its three icons and every language link.
//   display: none (hidden) takes the part of the other width out of the
//   accessibility tree as well, so a screen reader meets one of each.
// - GovueLanguageSwitch with linkFor: each language is a real link to this
//   page in that language, so it works without script and a crawler follows it.
//   A site with a router passes navigate as well, see the concept doc.
const props = defineProps<{
  /** The route path of the page without a language prefix, such as /contact. */
  path: string
  overlay?: boolean
}>()

const { locale } = useLanguage()

const items = computed<NavItem[]>(() => {
  const de = locale.value === 'de'
  const all: Array<[string, string, boolean]> = [
    ['/', de ? 'Start' : 'Home', false],
    ['/hero', 'Hero', false],
    ['/contact', de ? 'Kontakt' : 'Contact', true],
  ]
  return all.map(([path, label, cta]) => ({
    href: localizedPath(path, locale.value as 'de' | 'en'),
    label,
    current: path === props.path,
    ...(cta ? { variant: 'cta' as const } : {}),
  }))
})

function linkFor(code: string): string {
  return localizedPath(props.path, code as 'de' | 'en')
}
</script>

<template>
  <GovueHeader :overlay="overlay" nav-placement="end">
    <template #brand>
      <a :href="localizedPath('/', locale as 'de' | 'en')">govue example</a>
    </template>
    <GovueNav :items="items" />
    <template #theme>
      <span class="contents lg:hidden"><GovueThemeSwitch cycle /></span>
      <span class="hidden lg:contents"><GovueThemeSwitch compact /></span>
    </template>
    <template #actions>
      <span class="contents lg:hidden">
        <GovueLanguageSwitch :link-for="linkFor" others-only />
      </span>
      <span class="hidden lg:contents"><GovueLanguageSwitch :link-for="linkFor" /></span>
    </template>
  </GovueHeader>
</template>
