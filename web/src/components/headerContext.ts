// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import type { InjectionKey } from 'vue'

/**
 * What a navigation inside GovueHeader tells it. The header is transparent at
 * the top of an overlay page, and an open menu needs a solid ground to be
 * readable, so GovueNav reports whether its list is open. A navigation outside
 * a header finds no context and reports nothing.
 */
export interface HeaderContext {
  setMenuOpen: (open: boolean) => void
}

export const headerContextKey: InjectionKey<HeaderContext> = Symbol('govue-header')
