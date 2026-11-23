// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import de from './de'
import deFormal from './deFormal'
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

/** How a catalogue addresses the visitor. */
export type Formality = 'informal' | 'formal'

/**
 * The formal variant of a language whose default catalogue is informal, with
 * the same keys. It is a register of a language, not a language: it is not in
 * catalogues, so the switch, the routes and lang never list it, and
 * createGovueI18n({ formality: 'formal' }) loads it under the language's own
 * code. English has one register and no entry here.
 */
export const formalCatalogues = {
  de: deFormal,
} as const satisfies Partial<Record<LocaleCode, MessageTree>>

/** The catalogue of a language in a register, the default one when the language has no variant. */
export function catalogueFor(code: LocaleCode, formality: Formality = 'informal'): MessageTree {
  if (formality === 'formal' && Object.hasOwn(formalCatalogues, code)) {
    return formalCatalogues[code as keyof typeof formalCatalogues]
  }
  return catalogues[code].messages
}

/** The language when nothing is stored and the browser names none we have. */
export const DEFAULT_LOCALE: LocaleCode = 'de'

export function isLocaleCode(value: unknown): value is LocaleCode {
  return typeof value === 'string' && Object.hasOwn(catalogues, value)
}
