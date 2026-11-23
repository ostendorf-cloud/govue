// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'

import {
  catalogueFor,
  catalogues,
  formalCatalogues,
  isLocaleCode,
  localeCodes,
  type LocaleCode,
} from './catalogues'
import type { MessageTree } from './messages'

/** Every leaf as "a.b.c" mapped to its text. */
function leaves(tree: MessageTree, prefix = ''): Map<string, string> {
  const out = new Map<string, string>()
  for (const [key, value] of Object.entries(tree)) {
    const path = prefix ? `${prefix}.${key}` : key
    if (typeof value === 'string') {
      out.set(path, value)
    } else {
      for (const [p, v] of leaves(value, path)) {
        out.set(p, v)
      }
    }
  }
  return out
}

function placeholders(text: string): string[] {
  return [...text.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort()
}

const reference = localeCodes[0]

describe('catalogues', () => {
  it('lists at least German and English', () => {
    expect(localeCodes).toEqual(expect.arrayContaining(['de', 'en']))
  })

  it('keeps every key under the govue namespace', () => {
    for (const code of localeCodes) {
      expect(Object.keys(catalogues[code].messages)).toEqual(['govue'])
    }
  })

  for (const code of localeCodes.slice(1)) {
    describe(`${reference} against ${code}`, () => {
      const a = leaves(catalogues[reference].messages)
      const b = leaves(catalogues[code].messages)

      it('has the same keys, nested ones included', () => {
        expect([...b.keys()].sort()).toEqual([...a.keys()].sort())
      })

      it('uses the same named placeholders in every message', () => {
        for (const [key, text] of a) {
          expect(placeholders(b.get(key) ?? ''), key).toEqual(placeholders(text))
        }
      })
    })
  }

  it('has no empty message', () => {
    for (const code of localeCodes) {
      for (const [key, text] of leaves(catalogues[code].messages)) {
        expect(text.trim(), `${code}: ${key}`).not.toBe('')
      }
    }
  })

  it('names every language in its own language', () => {
    expect(catalogues.de.name).toBe('Deutsch')
    expect(catalogues.en.name).toBe('English')
  })
})

/** The words of the informal German register: du and its forms. */
const INFORMAL_PRONOUN = /(^|[^\p{L}])(du|dein\p{L}*|dir|dich)(?=$|[^\p{L}])/iu

/**
 * The informal imperatives the German catalogue uses. A formal text has none.
 * Case sensitive: "Versuche" and "Stimme" with a capital are nouns, so only
 * the other verbs count at the start of a sentence.
 */
const INFORMAL_IMPERATIVE =
  /(^|[^\p{L}])(versuche|prüfe|lade|melde|fülle|nutze|stimme|bewahre|füge|gib|warte|sende|schreibe|Prüfe|Lade|Melde|Fülle|Nutze|Bewahre|Füge|Gib|Warte|Sende|Schreibe)(?=$|[^\p{L}])/u

describe('formal catalogues', () => {
  it('are no language of their own', () => {
    expect(localeCodes).not.toContain('de-formal')
    for (const code of Object.keys(formalCatalogues)) {
      expect(isLocaleCode(code), code).toBe(true)
    }
  })

  for (const [code, formal] of Object.entries(formalCatalogues) as Array<
    [LocaleCode, MessageTree]
  >) {
    describe(`${code} formal against ${code}`, () => {
      const informal = leaves(catalogues[code].messages)
      const variant = leaves(formal)

      it('has the same keys, nested ones included, in both directions', () => {
        expect([...variant.keys()].filter((key) => !informal.has(key))).toEqual([])
        expect([...informal.keys()].filter((key) => !variant.has(key))).toEqual([])
      })

      it('uses the same named placeholders in every message', () => {
        for (const [key, text] of informal) {
          expect(placeholders(variant.get(key) ?? ''), key).toEqual(placeholders(text))
        }
      })

      it('has no empty message', () => {
        for (const [key, text] of variant) {
          expect(text.trim(), key).not.toBe('')
        }
      })
    })
  }

  it('addresses the visitor as Sie in German, never as du', () => {
    for (const [key, text] of leaves(formalCatalogues.de)) {
      expect(text, key).not.toMatch(INFORMAL_PRONOUN)
      expect(text, key).not.toMatch(INFORMAL_IMPERATIVE)
    }
  })

  it('changes every German text that says du, and only the register', () => {
    const informal = leaves(catalogues.de.messages)
    const formal = leaves(formalCatalogues.de)
    for (const [key, text] of informal) {
      if (INFORMAL_PRONOUN.test(text) || INFORMAL_IMPERATIVE.test(text)) {
        expect(formal.get(key), key).not.toBe(text)
        expect(formal.get(key), key).toMatch(/(^|[^\p{L}])(Sie|Ihr\p{L}*)(?=$|[^\p{L}])/u)
      } else {
        expect(formal.get(key), key).toBe(text)
      }
    }
  })

  it('has the informal register as the default', () => {
    expect(catalogueFor('de')).toBe(catalogues.de.messages)
    expect(catalogueFor('de', 'informal')).toBe(catalogues.de.messages)
    expect(catalogueFor('de', 'formal')).toBe(formalCatalogues.de)
    // English has one register.
    expect(catalogueFor('en', 'formal')).toBe(catalogues.en.messages)
  })
})
