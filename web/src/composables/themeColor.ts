// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import type { ColorMode } from './useTheme'

/**
 * The attribute a theme-color tag keeps its own colour in, written by the
 * boot script of the Go package themeboot or by pinThemeColor, whichever
 * runs first. A test checks the boot script against this constant.
 */
export const THEME_COLOR_ATTRIBUTE = 'data-govue-color'

/**
 * pinThemeColor sets the browser bar colour for a mode. With one
 * theme-color tag per mode (media `(prefers-color-scheme: light)` and
 * `dark`), light and dark give every tag the colour of that mode's tag, and
 * system gives each tag its own colour back, so the browser follows the
 * system again. A tag without a media query keeps its colour. It is the step
 * the boot script runs at load, for a later change of the mode.
 */
export function pinThemeColor(mode: ColorMode, doc: Document = document): void {
  const tags = Array.from(doc.querySelectorAll<HTMLMetaElement>('meta[name="theme-color"]'))
  const own = (tag: HTMLMetaElement): string => {
    if (!tag.hasAttribute(THEME_COLOR_ATTRIBUTE)) {
      tag.setAttribute(THEME_COLOR_ATTRIBUTE, tag.getAttribute('content') ?? '')
    }
    return tag.getAttribute(THEME_COLOR_ATTRIBUTE) ?? ''
  }
  const media = `(prefers-color-scheme: ${mode})`
  let colour = ''
  for (const tag of tags) {
    const value = own(tag)
    if (mode !== 'system' && tag.getAttribute('media') === media) {
      colour = value
    }
  }
  for (const tag of tags) {
    tag.setAttribute('content', colour !== '' ? colour : own(tag))
  }
}
