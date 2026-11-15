// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

/**
 * The names useTheme and the boot script share. The boot script is the Go
 * package themeboot, which sets the attributes before the first paint; a test
 * runs that script and checks it against these constants, so the two cannot
 * drift apart.
 */

/** The localStorage key the choice is kept under. */
export const THEME_STORAGE_KEY = 'govue-theme'

/** The root attribute that picks the palette. */
export const THEME_ATTRIBUTE = 'data-theme'

/** The root attribute that pins light or dark. Absent means the system decides. */
export const MODE_ATTRIBUTE = 'data-mode'
