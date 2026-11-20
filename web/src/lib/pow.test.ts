// @vitest-environment node
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { createHash } from 'node:crypto'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

import { describe, expect, it, vi } from 'vitest'

import type { ApiClient } from '../api/client'
import { createChallengeSolver, parseSalt, type PowChallenge, type SolveFunction } from './pow'
import { MAX_NUMBER_LIMIT, solve } from './powSolve'

interface Vectors {
  hashes: Array<{ salt: string; number: number; challenge: string }>
  cases: Array<{ name: string; solution: { salt: string; number: number; challenge: string } }>
}

// The file the Go package writes. Both solvers answer to it.
const vectors = JSON.parse(
  readFileSync(
    fileURLToPath(new URL('../../../pow/testdata/vectors.json', import.meta.url)),
    'utf8',
  ),
) as Vectors

describe('solve against the shared vectors', () => {
  for (const vector of vectors.hashes) {
    it(`finds ${vector.number} for the salt ${JSON.stringify(vector.salt.slice(-24))}`, async () => {
      expect(await solve(vector.challenge, vector.salt, 10_000)).toBe(vector.number)
    })
  }

  it('hashes salt plus the decimal number with nothing in between', async () => {
    const salt = '00112233445566778899aabb?expires=1790000600&issued=1790000000&'
    const challenge = createHash('sha256').update(`${salt}12`).digest('hex')
    expect(await solve(challenge, salt, 100)).toBe(12)
  })

  it('returns null when no number up to the limit solves it', async () => {
    const [vector] = vectors.hashes
    expect(await solve(vector.challenge, vector.salt, 100)).toBeNull()
  })

  it('refuses a challenge that is not 64 lower case hex digits', async () => {
    expect(await solve('abc', 'salt&', 10)).toBeNull()
    expect(await solve('G'.repeat(64), 'salt&', 10)).toBeNull()
    expect(await solve('A'.repeat(64), 'salt&', 10)).toBeNull()
  })

  it('refuses a difficulty the Go package would refuse', async () => {
    const challenge = 'a'.repeat(64)
    expect(await solve(challenge, 's&', -1)).toBeNull()
    expect(await solve(challenge, 's&', 1.5)).toBeNull()
    expect(await solve(challenge, 's&', MAX_NUMBER_LIMIT + 1)).toBeNull()
  })
})

describe('parseSalt against the shared vectors', () => {
  it('refuses every salt of the vectors that lacks the separator', () => {
    let refused = 0
    for (const { name, solution } of vectors.cases) {
      if (!solution.salt.endsWith('&')) {
        refused++
        expect(() => parseSalt(solution.salt), name).toThrow()
      }
    }
    expect(refused).toBeGreaterThan(2)
  })

  it('takes the salt of the valid vector', () => {
    const valid = vectors.cases.find((c) => c.name === 'valid')
    expect(valid).toBeDefined()
    expect(() => parseSalt(valid?.solution.salt ?? '')).not.toThrow()
  })

  it('reads expires and issued', () => {
    expect(parseSalt('00112233445566778899aabb?expires=1790000600&issued=1790000000&')).toEqual({
      expires: 1790000600,
      issued: 1790000000,
    })
  })

  it('refuses another shape', () => {
    const nonce = '00112233445566778899aabb'
    for (const salt of [
      `${nonce}?issued=1&expires=2&`,
      `${nonce.toUpperCase()}?expires=2&issued=1&`,
      `${nonce}?expires=02&issued=1&`,
      `${nonce}?expires=2&issued=1&&`,
      `${nonce}?expires=2&issued=-1&`,
      `${nonce.slice(2)}?expires=2&issued=1&`,
    ]) {
      expect(() => parseSalt(salt), salt).toThrow()
    }
  })
})

/** A challenge the way pow.Service.Create builds it, with a small number. */
function challengeOf(issued: number, lifetime = 600, secret = 7): PowChallenge {
  const salt = `00112233445566778899aabb?expires=${issued + lifetime}&issued=${issued}&`
  return {
    algorithm: 'SHA-256',
    challenge: createHash('sha256').update(`${salt}${secret}`).digest('hex'),
    maxnumber: 50,
    salt,
    signature: 'f'.repeat(64),
  }
}

function setup(options: { lifetime?: number } = {}) {
  let clock = 1_000_000
  const get = vi.fn<(path: string, options?: unknown) => Promise<PowChallenge>>(() =>
    Promise.resolve(challengeOf(Math.floor(clock / 1000), options.lifetime)),
  )
  const client = { get } as unknown as ApiClient
  const slept: number[] = []
  const solveFn = vi.fn<SolveFunction>((c) => solve(c.challenge, c.salt, c.maxnumber))
  const solver = createChallengeSolver(client, {
    solve: solveFn,
    now: () => clock,
    sleep: (ms) => {
      slept.push(ms)
      clock += ms
      return Promise.resolve()
    },
  })
  return {
    solver,
    get,
    solveFn,
    slept,
    advance: (ms: number) => {
      clock += ms
    },
  }
}

describe('createChallengeSolver', () => {
  it('fetches on prepare and not before', () => {
    const { solver, get } = setup()
    expect(get).not.toHaveBeenCalled()
    solver.prepare()
    expect(get).toHaveBeenCalledTimes(1)
    expect(get).toHaveBeenCalledWith('/contact/challenge', { cache: 'no-store' })
    solver.prepare()
    solver.prepare()
    expect(get).toHaveBeenCalledTimes(1)
  })

  it('answers with the fields of pow.Solution and the number it found', async () => {
    const { solver } = setup()
    solver.prepare()
    const solution = await solver.solved()
    expect(Object.keys(solution).sort()).toEqual([
      'algorithm',
      'challenge',
      'number',
      'salt',
      'signature',
    ])
    expect(solution.number).toBe(7)
    expect(solution.algorithm).toBe('SHA-256')
  })

  it('waits out the minimum age from the arrival of the challenge', async () => {
    const { solver, slept, advance } = setup()
    solver.prepare()
    await new Promise((resolve) => setTimeout(resolve, 5))
    advance(1000)
    await solver.solved()
    expect(slept).toEqual([2500])
  })

  it('does not wait when the challenge is old enough', async () => {
    const { solver, slept, advance } = setup()
    solver.prepare()
    await Promise.resolve()
    await new Promise((resolve) => setTimeout(resolve, 5))
    advance(4000)
    await solver.solved()
    expect(slept).toEqual([])
  })

  it('fetches when solved is called without prepare', async () => {
    const { solver, get } = setup()
    await solver.solved()
    expect(get).toHaveBeenCalledTimes(1)
  })

  it('uses a solution once: the next one is a new challenge', async () => {
    const { solver, get } = setup()
    solver.prepare()
    await solver.solved()
    await solver.solved()
    expect(get).toHaveBeenCalledTimes(2)
  })

  it('refetches a challenge that is within 60 seconds of its expiry', async () => {
    const { solver, get, advance } = setup({ lifetime: 600 })
    solver.prepare()
    await Promise.resolve()
    await new Promise((resolve) => setTimeout(resolve, 5))
    advance((600 - 59) * 1000)
    await solver.solved()
    expect(get).toHaveBeenCalledTimes(2)
  })

  it('keeps a challenge that is more than 60 seconds from its expiry', async () => {
    const { solver, get, advance } = setup({ lifetime: 600 })
    solver.prepare()
    await Promise.resolve()
    await new Promise((resolve) => setTimeout(resolve, 5))
    advance((600 - 61) * 1000)
    await solver.solved()
    expect(get).toHaveBeenCalledTimes(1)
  })

  it('prepare replaces a challenge that is about to expire', async () => {
    const { solver, get, advance } = setup({ lifetime: 600 })
    solver.prepare()
    await new Promise((resolve) => setTimeout(resolve, 5))
    advance(550 * 1000)
    solver.prepare()
    expect(get).toHaveBeenCalledTimes(2)
  })

  it('retries one failed fetch and then gives up', async () => {
    const { solver, get } = setup()
    get.mockRejectedValueOnce(new Error('offline'))
    await expect(solver.solved()).resolves.toMatchObject({ number: 7 })
    get.mockRejectedValue(new Error('offline'))
    await expect(solver.solved()).rejects.toThrow('offline')
    expect(get).toHaveBeenCalledTimes(4)
  })

  it('rejects when no number solves the challenge', async () => {
    const { solver, solveFn } = setup()
    solveFn.mockResolvedValue(null)
    await expect(solver.solved()).rejects.toThrow(/no number/)
  })

  it('rejects a salt without the separator', async () => {
    const { solver, get } = setup()
    get.mockResolvedValue({
      ...challengeOf(1000),
      salt: '00112233445566778899aabb?expires=1600&issued=1000',
    })
    await expect(solver.solved()).rejects.toThrow(/salt/)
  })

  it('takes the challenge route from the options', async () => {
    const get = vi.fn(() => Promise.resolve(challengeOf(1000)))
    const solver = createChallengeSolver({ get } as unknown as ApiClient, {
      path: '/c',
      solve: () => Promise.resolve(7),
      sleep: () => Promise.resolve(),
    })
    solver.prepare()
    expect(get).toHaveBeenCalledWith('/c', { cache: 'no-store' })
    await solver.solved()
  })
})
