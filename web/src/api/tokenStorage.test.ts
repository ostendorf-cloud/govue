// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from 'vitest'

import { createMemoryTokenStorage } from './tokenStorage'

describe('createMemoryTokenStorage', () => {
  it('starts empty, keeps what it is given and forgets on clear', async () => {
    const storage = createMemoryTokenStorage()

    expect(await storage.get()).toBeNull()
    await storage.set('value')
    expect(await storage.get()).toBe('value')
    await storage.clear()
    expect(await storage.get()).toBeNull()
  })

  it('shares nothing between two instances', async () => {
    const a = createMemoryTokenStorage()
    const b = createMemoryTokenStorage()

    await a.set('value')

    expect(await b.get()).toBeNull()
  })
})
