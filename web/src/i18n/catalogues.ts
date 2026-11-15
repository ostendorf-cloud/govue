// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import de from './de'
import en from './en'
import type { MessageTree } from './messages'

/**
 * The one place the languages are listed. The plugin, the switch and the
 * completeness test all read this object. A third language is one catalogue
 * file next to de.ts and en.ts plus one line here; no component changes.
 *
 * name is the language written in its own language, so a visitor who does not
 * read the current one still finds theirs.
 */
export const catalogues = {
  de: { name: 'Deutsch', messages: de },
  en: { name: 'English', messages: en },
} as const satisfies Record<string, { name: string; messages: MessageTree }>

export type LocaleCode = keyof typeof catalogues

export const localeCodes = Object.keys(catalogues) as LocaleCode[]

/** The language when nothing is stored and the browser names none we have. */
export const DEFAULT_LOCALE: LocaleCode = 'de'

export function isLocaleCode(value: unknown): value is LocaleCode {
  return typeof value === 'string' && Object.hasOwn(catalogues, value)
}
