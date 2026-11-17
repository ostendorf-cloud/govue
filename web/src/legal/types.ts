// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import type { LocaleCode } from '../i18n/catalogues'

/**
 * One text in every language the library ships. The record is keyed by the
 * catalogue list, so a new language is a type error here until every legal
 * text has it. The German text is the binding one.
 */
export type LocalizedText = Record<LocaleCode, string>

/** A paragraph, or a bullet list. */
export type LegalBlock =
  | {
      kind: 'p'
      text: LocalizedText
      /** Set in capitals, as the GDPR objection notice conventionally is. */
      upper?: boolean
    }
  | { kind: 'list'; items: LocalizedText[] }

/**
 * A titled part of a legal page. Level 3 is a section under a numbered part,
 * level 4 a question inside one.
 */
export interface LegalSection {
  /** Used as the anchor on the page. */
  id: string
  level: 3 | 4
  heading: LocalizedText
  blocks: LegalBlock[]
}

/**
 * An optional building block for the part "data collection on this website".
 * It is a section that also says what it stores and for how long. Both fields
 * are rendered into the page, and a test fails when one is empty.
 */
export interface DataSection extends LegalSection {
  stores: LocalizedText[]
  retention: LocalizedText
}

/**
 * The provider of every page, configured once per site. Nothing in this
 * package carries a real provider.
 */
export interface LegalProvider {
  name: string
  street: string
  postalCode: string
  city: string
  /** Written after the city, for example Deutschland and Germany. */
  country?: LocalizedText
  phone: string
  email: string
  vatId: string
  /** The consumer dispute resolution statement, which differs per provider. */
  disputeResolution: LocalizedText
}
