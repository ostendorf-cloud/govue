// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import type { LegalBlock, LegalSection, LocalizedText } from './types'

/** tx builds a text. German comes first because it is the binding one. */
export function tx(de: string, en: string): LocalizedText {
  return { de, en }
}

/** p is a paragraph. */
export function p(de: string, en: string, upper = false): LegalBlock {
  return upper ? { kind: 'p', text: tx(de, en), upper } : { kind: 'p', text: tx(de, en) }
}

/** ul is a bullet list, one pair of texts per item. */
export function ul(...items: Array<[string, string]>): LegalBlock {
  return { kind: 'list', items: items.map(([de, en]) => tx(de, en)) }
}

/** section is a titled part. Level 3 is the default. */
export function section(
  id: string,
  heading: [string, string],
  blocks: LegalBlock[],
  level: 3 | 4 = 3,
): LegalSection {
  return { id, level, heading: tx(heading[0], heading[1]), blocks }
}
