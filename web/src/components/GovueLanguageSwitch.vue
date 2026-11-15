<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { useLanguage, useGovueText } from '../i18n/plugin'

// A native select, not a radio group like GovueThemeSwitch. The list of
// languages is open ended (a third catalogue adds an option, not a button), a
// select stays one control at any length, and the platform gives it the
// keyboard handling, the screen reader announcement and the touch picker for
// free. Focus never leaves it: changing the value does not re-render the
// element. The page is not reloaded and nothing navigates, so the visitor
// stays where they are.
//
// Each option is written in its own language and carries its own lang
// attribute, so a screen reader pronounces "Deutsch" in German. The group name
// follows the active language.
defineProps<{
  /** The accessible name. Defaults to the catalogue's, in the active language. */
  label?: string
}>()

const emit = defineEmits<{ change: [code: string] }>()

const { locale, locales, setLocale } = useLanguage()
const { t } = useGovueText()

function onChange(event: Event): void {
  const code = (event.target as HTMLSelectElement).value
  setLocale(code)
  emit('change', code)
}
</script>

<template>
  <select
    :aria-label="label ?? t('govue.language.label')"
    :value="locale"
    class="min-h-11 min-w-11 rounded-md border border-border-strong bg-surface px-3 text-base text-text transition-colors hover:bg-surface-hover"
    @change="onChange"
  >
    <option
      v-for="entry in locales"
      :key="entry.code"
      :value="entry.code"
      :lang="entry.code"
      :selected="entry.code === locale"
    >
      {{ entry.name }}
    </option>
  </select>
</template>
