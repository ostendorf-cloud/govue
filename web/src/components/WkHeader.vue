<template>
  <!-- The one banner landmark. It stays put, so the navigation is reachable
       from anywhere on a long page. -->
  <header class="sticky top-0 z-10 border-b border-border bg-surface">
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

      <div class="order-3 w-full min-[480px]:order-2 min-[480px]:w-auto min-[480px]:flex-1">
        <slot />
      </div>

      <div class="order-2 ml-auto flex items-center gap-2 min-[480px]:order-3">
        <slot name="actions" />
      </div>
    </div>
  </header>
</template>
