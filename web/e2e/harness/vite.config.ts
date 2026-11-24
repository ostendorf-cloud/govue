// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { fileURLToPath, URL } from 'node:url'

import tailwindcss from '@tailwindcss/vite'
import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vite'

// A standalone dev server for the Playwright harness pages. It shares the
// package's pinned Vue and Vite versions but is otherwise unrelated to the
// library build in ../../vite.config.ts. Most pages import no stylesheet:
// they only need the components to render, not their token classes to
// resolve to anything. The header page measures layout, so it imports
// header.css, which the Tailwind plugin builds from the real classes.
export default defineConfig({
  root: fileURLToPath(new URL('.', import.meta.url)),
  plugins: [vue(), tailwindcss()],
  server: {
    port: 4173,
    strictPort: true,
    // The contact spec runs against the Go example server (see
    // playwright.config.ts). The page calls /api on its own origin and Vite
    // forwards it, with the Host header unchanged, so the server's origin
    // check sees a same origin request.
    proxy: { '/api': { target: 'http://127.0.0.1:18080' } },
  },
})
