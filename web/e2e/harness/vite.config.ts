// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { fileURLToPath, URL } from 'node:url'

import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vite'

// A standalone dev server for the Playwright harness page. It shares the
// package's pinned Vue and Vite versions but is otherwise unrelated to the
// library build in ../../vite.config.ts: no Tailwind plugin, since the
// harness only needs the components to render, not their token classes to
// resolve to anything.
export default defineConfig({
  root: fileURLToPath(new URL('.', import.meta.url)),
  plugins: [vue()],
  server: {
    port: 4173,
    strictPort: true,
  },
})
