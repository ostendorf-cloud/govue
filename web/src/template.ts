// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

/**
 * The template placeholder logic of the render entry, in its own module so
 * the render entry and localizedFiles share it without importing each other.
 * web/scripts/prerender.mjs holds a plain JavaScript copy, and a test runs
 * the same cases through both.
 */

/** The default marker `injectIntoTemplate` looks for. */
export const DEFAULT_PLACEHOLDER = '<!--app-html-->'

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
