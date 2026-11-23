<!--
Copyright 2026 Simon Ostendorf
SPDX-License-Identifier: Apache-2.0
-->

<script setup lang="ts">
import { computed, provide, ref } from 'vue'

import { useScrolled } from '../composables/useScrolled'
import GovueThemeSwitch from './GovueThemeSwitch.vue'
import { headerContextKey } from './headerContext'

/** Where the navigation sits in the row. `start` is next to the brand, as the
 *  header always was, `end` is next to the actions. */
export type NavPlacement = 'start' | 'center' | 'end'

const props = withDefaults(
  defineProps<{
    /** Shows the theme switch in the actions area. Off by default. */
    themeSwitch?: boolean
    /**
     * Takes the header out of the flow, so the page content starts under it.
     * It is transparent, without border and blur, until the page has scrolled
     * a little, then takes the solid ground. The page owns the space under
     * the header: a hero fills it, other content needs its own top padding.
     * The transparent state shows the dark mode roles, so the hero behind it
     * has to be dark. Off by default.
     */
    overlay?: boolean
    /** Where the navigation slot sits in the row. Default `start`. */
    navPlacement?: NavPlacement
  }>(),
  { themeSwitch: false, overlay: false, navPlacement: 'start' },
)

// Nothing is read or listened to unless the header overlays, and on the server
// the state stays false: the prerendered page is the transparent one.
const scrolled = useScrolled({ active: () => props.overlay })
const menuOpen = ref(false)

// An open menu lists links over whatever is behind the header, so it always
// sits on the solid ground.
provide(headerContextKey, {
  setMenuOpen: (open) => {
    menuOpen.value = open
  },
})

const solid = computed(() => !props.overlay || scrolled.value || menuOpen.value)

// Whole class names, so Tailwind finds them. The colour scheme of the
// transparent state is dark: the roles then resolve to their dark values, the
// ones made for a dark ground, whatever mode the page is in.
const ground = computed(() =>
  solid.value
    ? 'border-header-border bg-header-bg backdrop-blur-header'
    : 'border-transparent bg-transparent [color-scheme:dark]',
)

const placements: Record<NavPlacement, string> = {
  start: '',
  center: 'min-[480px]:flex min-[480px]:justify-center',
  end: 'min-[480px]:flex min-[480px]:justify-end',
}
</script>

<template>
  <!-- The one banner landmark. It stays put, so the navigation is reachable
       from anywhere on a long page. The border keeps its width in every
       state, so the header never changes size when it turns solid. A site
       styles it through the --color-header-bg, --color-header-border and
       --blur-header tokens. data-state is the hook for a site's own rules. -->
  <header
    :data-state="solid ? 'solid' : 'transparent'"
    :data-overlay="overlay ? 'true' : undefined"
    :class="[
      'inset-x-0 top-0 z-10 border-b transition-colors motion-reduce:transition-none',
      overlay ? 'fixed' : 'sticky',
      ground,
    ]"
  >
    <div
      :class="[
        'mx-auto flex max-w-shell flex-wrap items-center gap-x-4 gap-y-1 px-4 py-2 sm:px-6',
        // Below 480px the three parts take two rows instead of pushing the page
        // sideways.
        'min-[480px]:h-header min-[480px]:flex-nowrap min-[480px]:py-0',
      ]"
    >
      <div class="order-1 flex min-h-11 items-center text-lg font-semibold text-text">
        <slot name="brand" />
      </div>

      <div
        :class="[
          'order-3 w-full min-[480px]:order-2 min-[480px]:w-auto min-[480px]:flex-1',
          placements[navPlacement],
        ]"
      >
        <slot />
      </div>

      <div class="order-2 ml-auto flex items-center gap-2 min-[480px]:order-3">
        <!-- Optional theme area. The switch is its default content, shown when
             themeSwitch is set; a site can fill the slot with its own. -->
        <slot v-if="themeSwitch || $slots.theme" name="theme">
          <GovueThemeSwitch />
        </slot>
        <slot name="actions" />
      </div>
    </div>
  </header>
</template>
