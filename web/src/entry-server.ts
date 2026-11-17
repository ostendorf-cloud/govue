// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

/**
 * The render entry for build time pre-rendering. It is published as
 * `@ostendorf-cloud/govue/server`, apart from the main entry, so a browser
 * bundle never pulls the server renderer in.
 *
 * The contract is small on purpose:
 *
 *   - render takes a root component and returns its HTML as a string.
 *   - It knows no route, no language and no template. A caller that renders
 *     one page per language installs the language in `setup` and puts lang,
 *     canonical and hreflang into the template itself.
 *   - It runs in Node with no DOM. It never touches a global and keeps no
 *     state of its own. Two composables do keep module level state: useTheme
 *     and useToasts. A toast shown while rendering would leak into the next
 *     render, and its pending timer would keep the build process alive, so a
 *     page must not call notify during render.
 *   - The client starts over the markup with createApp, never createSSRApp:
 *     the markup knows no stored choice and no browser language.
 *   - An error in a component rejects the promise. Nothing is swallowed, so a
 *     broken page fails the build instead of shipping empty.
 *
 * This is build time rendering of one page, not request time SSR.
 */

import { createSSRApp, type App, type Component } from 'vue'
import { renderToString } from 'vue/server-renderer'

export interface RenderOptions {
  /** Props for the root component. */
  props?: Record<string, unknown>
  /**
   * Runs on the app before it renders: install plugins here, such as
   * `app.use(createGovueI18n({ locale: 'en' }))`.
   */
  setup?: (app: App) => void | Promise<void>
}

/** The default marker `injectIntoTemplate` looks for. */
export const DEFAULT_PLACEHOLDER = '<!--app-html-->'

/** render returns the HTML of the root component as a string. */
export async function render(root: Component, options: RenderOptions = {}): Promise<string> {
  const app = createSSRApp(root, options.props ?? null)
  await options.setup?.(app)
  return renderToString(app)
}

/**
 * injectIntoTemplate puts the rendered markup in the place of the
 * placeholder. The placeholder has to appear exactly once: a template without
 * it, or with it twice, is a mistake the build must see.
 */
export function injectIntoTemplate(
  template: string,
  html: string,
  placeholder: string = DEFAULT_PLACEHOLDER,
): string {
  const first = template.indexOf(placeholder)
  if (placeholder === '' || first === -1) {
    throw new Error(
      `govue prerender: the template has no placeholder ${JSON.stringify(placeholder)}`,
    )
  }
  // From first + 1, so two overlapping matches count as two.
  if (template.indexOf(placeholder, first + 1) !== -1) {
    throw new Error(
      `govue prerender: the placeholder ${JSON.stringify(placeholder)} appears more than once`,
    )
  }
  // Slicing instead of String.replace: the markup may hold "$&" and friends.
  return template.slice(0, first) + html + template.slice(first + placeholder.length)
}
