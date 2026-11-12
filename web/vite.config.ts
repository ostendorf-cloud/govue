// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { fileURLToPath, URL } from 'node:url'

import tailwindcss from '@tailwindcss/vite'
import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vitest/config'

// Consumers import the TypeScript sources, so this build is not what they
// install: it is the check that the package builds standalone, and the bundle
// for a consumer that cannot compile Vue sources itself.
export default defineConfig({
  plugins: [vue(), tailwindcss()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  build: {
    lib: {
      entry: fileURLToPath(new URL('./src/index.ts', import.meta.url)),
      formats: ['es'],
      fileName: 'web-kit',
    },
    // Vue and Tailwind come from the consuming app, never twice.
    rollupOptions: {
      external: ['vue'],
      output: { globals: { vue: 'Vue' } },
    },
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./vitest.setup.ts'],
    // scripts/ carries the CLI wrapper's own test alongside its source, not
    // under src/, since it ships to a consumer's devDependency tree only
    // through theme:generate and never through the package's own exports.
    include: ['src/**/*.test.ts', 'scripts/**/*.test.ts'],
    css: false,
  },
})
