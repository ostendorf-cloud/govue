<script setup lang="ts">
export interface FooterLink {
  href: string
  label: string
}

withDefaults(
  defineProps<{
    /** Imprint, privacy, and whatever else the law asks for. */
    links?: FooterLink[]
    /** The copyright line, or anything else that belongs under the links. */
    note?: string
    /** The footer navigation needs a name of its own, next to the main one. */
    ariaLabel?: string
  }>(),
  { links: () => [], note: undefined, ariaLabel: 'Footer' },
)
</script>

<template>
  <footer class="border-t border-border bg-surface">
    <div class="mx-auto flex max-w-shell flex-col gap-4 px-4 py-8 sm:px-6">
      <div v-if="$slots.default" class="text-base text-text">
        <slot />
      </div>

      <nav v-if="links.length > 0" :aria-label="ariaLabel">
        <ul class="flex flex-wrap items-center gap-x-4">
          <li v-for="link in links" :key="link.href">
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
