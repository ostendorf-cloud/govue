<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { computed, inject } from 'vue'

import { isLocaleCode } from '../i18n/catalogues'
import { legalLinks, type LegalPage } from '../i18n/legal'
import { languageKey, useGovueText } from '../i18n/plugin'

export interface FooterLink {
  href: string
  label: string
}

const props = withDefaults(
  defineProps<{
    /** Further links, after the legal pages. */
    links?: FooterLink[]
    /**
     * The legal pages of the site, such as `['imprint', 'privacy']`. Each is
     * linked first, in the active language, at its canonical path.
     */
    legal?: readonly LegalPage[]
    /** The copyright line, or anything else that belongs under the links. */
    note?: string
    /** The footer navigation needs a name of its own, next to the main one. */
    ariaLabel?: string
  }>(),
  { links: () => [], legal: () => [], note: undefined, ariaLabel: undefined },
)

const language = inject(languageKey, null)
const { t } = useGovueText()

// Without the language layer the texts are English, so the links are too.
const allLinks = computed<FooterLink[]>(() => {
  const code = language?.locale.value
  const locale = isLocaleCode(code) ? code : 'en'
  return [...legalLinks(locale, props.legal, t), ...props.links]
})
</script>

<template>
  <footer class="border-t border-border bg-surface">
    <div class="mx-auto flex max-w-shell flex-col gap-4 px-4 py-8 sm:px-6">
      <div v-if="$slots.default" class="text-base text-text">
        <slot />
      </div>

      <nav v-if="allLinks.length > 0" :aria-label="ariaLabel ?? t('govue.footer.label')">
        <ul class="flex flex-wrap items-center gap-x-4">
          <li v-for="link in allLinks" :key="link.href">
            <a
              :href="link.href"
              class="inline-flex min-h-11 items-center text-sm text-accent-text transition-colors hover:underline"
            >
              {{ link.label }}
            </a>
          </li>
        </ul>
      </nav>

      <p v-if="note" class="text-sm text-text-muted">{{ note }}</p>
    </div>
  </footer>
</template>
