<script setup lang="ts">
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { computed } from 'vue'

import { useLanguage, useGovueText } from '../i18n/plugin'

// Two modes.
//
// The default is a native select, not a radio group like GovueThemeSwitch. The
// list of languages is open ended (a third catalogue adds an option, not a
// button), a select stays one control at any length, and the platform gives it
// the keyboard handling, the screen reader announcement and the touch picker
// for free. Focus never leaves it: changing the value does not re-render the
// element. The page is not reloaded and nothing navigates, so the visitor
// stays where they are.
//
// With linkFor each language is a real link to the same page in that
// language, with hreflang, so a crawler and a visitor without script can
// follow it. The links sit in a named group, the current one carries
// aria-current, and a link is announced as a link, never as a radio. A plain
// click follows the href. When the app's router handles the path, navigate
// takes the click over: the locale changes and the page does not reload.
//
// With othersOnly, link mode leaves the current language out and renders only
// the links of the others: with two languages one link, which fits a narrow
// header. The links stay what they are, with hreflang, lang, linkFor and
// navigate, inside the same named group. A link is reused in place rather
// than replaced when the language changes, so a keyboard visitor who followed
// it through the router keeps focus on it.
//
// Each option is written in its own language and carries its own lang
// attribute, so a screen reader pronounces "Deutsch" in German. The group name
// follows the active language.
const props = withDefaults(
  defineProps<{
    /** The accessible name. Defaults to the catalogue's, in the active language. */
    label?: string
    /**
     * Turns the switch into a group of links. It maps a language code to the
     * path of the same page in that language, for example
     * `(code) => localizedPath(route.path, code)`. Without it the switch is
     * a select and nothing else changes.
     */
    linkFor?: (code: string) => string
    /**
     * Link mode only. Called with the href and the code on a plain click,
     * after the locale changed. Return true when the router took the
     * navigation over, for example `(href) => { router.push(href); return true }`:
     * the click is then cancelled, so the page does not reload. Return
     * anything else, or leave it out, and the browser follows the link.
     */
    navigate?: (href: string, code: string) => boolean | void
    /**
     * Link mode only. Renders the links of the other languages and leaves
     * the current one out. Off by default. The select ignores it.
     */
    othersOnly?: boolean
  }>(),
  { label: undefined, linkFor: undefined, navigate: undefined, othersOnly: false },
)

const emit = defineEmits<{ change: [code: string] }>()

const { locale, locales, setLocale } = useLanguage()
const { t } = useGovueText()

const links = computed(() =>
  props.othersOnly ? locales.filter((entry) => entry.code !== locale.value) : locales,
)

/** The key of a link. With othersOnly it is the position, so the element is
 *  patched in place when the language changes and keeps focus. */
function linkKey(code: string, index: number): string {
  return props.othersOnly ? `other-${index}` : code
}

function onChange(event: Event): void {
  const code = (event.target as HTMLSelectElement).value
  setLocale(code)
  emit('change', code)
}

/** isPlainClick is a primary click with no modifier: the ones that navigate in
 *  this tab. A new tab or window is the browser's, and the locale there is
 *  decided by the page it opens. */
function isPlainClick(event: MouseEvent): boolean {
  return (
    event.button === 0 &&
    !event.defaultPrevented &&
    !event.metaKey &&
    !event.ctrlKey &&
    !event.shiftKey &&
    !event.altKey
  )
}

function onLink(event: MouseEvent, code: string, href: string): void {
  if (!isPlainClick(event)) {
    return
  }
  setLocale(code)
  emit('change', code)
  if (props.navigate?.(href, code) === true) {
    event.preventDefault()
  }
}
</script>

<template>
  <div
    v-if="linkFor"
    role="group"
    :aria-label="label ?? t('govue.language.label')"
    class="inline-flex flex-wrap items-center gap-1"
  >
    <a
      v-for="(entry, index) in links"
      :key="linkKey(entry.code, index)"
      :href="linkFor(entry.code)"
      :hreflang="entry.code"
      :lang="entry.code"
      :aria-current="entry.code === locale ? 'true' : undefined"
      :class="[
        'inline-flex min-h-11 min-w-11 items-center justify-center rounded-md px-3 text-base transition-colors',
        entry.code === locale
          ? 'bg-accent-subtle font-semibold text-accent-text underline decoration-2 underline-offset-4'
          : 'text-text hover:bg-surface-hover',
      ]"
      @click="onLink($event, entry.code, linkFor(entry.code))"
    >
      {{ entry.name }}
    </a>
  </div>

  <select
    v-else
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
