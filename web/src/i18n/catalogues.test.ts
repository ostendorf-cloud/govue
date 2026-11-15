// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'

import { catalogues, localeCodes } from './catalogues'
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
