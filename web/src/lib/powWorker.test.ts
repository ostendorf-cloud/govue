// @vitest-environment node
// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import { createHash } from 'node:crypto'

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { ApiClient } from '../api/client'
import {
  ChallengeError,
  createChallengeSolver,
  solveInWorker,
  type PowChallenge,
  type SolveFunction,
} from './pow'

class StubWorker {
  static instances: StubWorker[] = []
  onmessage: ((event: { data: { number: number | null } }) => void) | null = null
  onerror: (() => void) | null = null
  onmessageerror: (() => void) | null = null
  terminated = 0
  posted: unknown[] = []
  constructor(
    readonly url: URL,
    readonly options: unknown,
  ) {
    StubWorker.instances.push(this)
  }
  postMessage(data: unknown) {
    this.posted.push(data)
  }
  terminate() {
    this.terminated++
  }
}

const challenge: PowChallenge = {
  algorithm: 'SHA-256',
  challenge: 'a'.repeat(64),
  maxnumber: 99,
  salt: '00112233445566778899aabb?expires=1790000600&issued=1790000000&',
  signature: 'b'.repeat(64),
}

beforeEach(() => {
  StubWorker.instances = []
  vi.stubGlobal('Worker', StubWorker)
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

describe('solveInWorker', () => {
  it('starts a module worker, posts the three fields and resolves with the number', async () => {
    const result = solveInWorker(challenge)
    const worker = StubWorker.instances[0]

    expect(StubWorker.instances).toHaveLength(1)
    expect(worker.options).toEqual({ type: 'module' })
    expect(worker.url.pathname).toMatch(/pow\.worker\.ts$/)
    expect(worker.posted).toEqual([
      { challenge: challenge.challenge, salt: challenge.salt, maxnumber: 99 },
    ])

    worker.onmessage?.({ data: { number: 42 } })

    await expect(result).resolves.toBe(42)
    expect(worker.terminated).toBe(1)
  })

  it('resolves null when the worker found no number, and terminates it', async () => {
    const result = solveInWorker(challenge)
    const worker = StubWorker.instances[0]

    worker.onmessage?.({ data: { number: null } })

    await expect(result).resolves.toBeNull()
    expect(worker.terminated).toBe(1)
  })

  it('rejects with a worker error and terminates the worker', async () => {
    const result = solveInWorker(challenge)
    const worker = StubWorker.instances[0]

    worker.onerror?.()

    await expect(result).rejects.toMatchObject({ name: 'ChallengeError', kind: 'worker' })
    expect(worker.terminated).toBe(1)
  })

  it('rejects when the worker cannot start', async () => {
    vi.stubGlobal(
      'Worker',
      class {
        constructor() {
          throw new Error('blocked by CSP')
        }
      },
    )
    await expect(solveInWorker(challenge)).rejects.toMatchObject({ kind: 'worker' })
  })

  it('times out a worker that never answers and terminates it', async () => {
    vi.useFakeTimers()
    const result = solveInWorker(challenge, undefined, 5000)
    const worker = StubWorker.instances[0]
    const settled = expect(result).rejects.toMatchObject({ kind: 'timeout' })

    await vi.advanceTimersByTimeAsync(4999)
    expect(worker.terminated).toBe(0)
    await vi.advanceTimersByTimeAsync(2)

    await settled
    expect(worker.terminated).toBe(1)
  })

  it('stops the timer once the worker answered', async () => {
    vi.useFakeTimers()
    const result = solveInWorker(challenge, undefined, 5000)
    StubWorker.instances[0].onmessage?.({ data: { number: 1 } })
    await result

    expect(vi.getTimerCount()).toBe(0)
  })

  it('terminates the worker when the signal aborts', async () => {
    const controller = new AbortController()
    const result = solveInWorker(challenge, controller.signal)
    const worker = StubWorker.instances[0]
    const settled = expect(result).rejects.toMatchObject({ kind: 'aborted' })

    controller.abort()

    await settled
    expect(worker.terminated).toBe(1)
  })

  it('does not start a worker for a signal that is already aborted', async () => {
    const controller = new AbortController()
    controller.abort()

    await expect(solveInWorker(challenge, controller.signal)).rejects.toMatchObject({
      kind: 'aborted',
    })
    expect(StubWorker.instances).toHaveLength(0)
  })
})

function challengeFor(issued: number, lifetime: number): PowChallenge {
  const salt = `00112233445566778899aabb?expires=${issued + lifetime}&issued=${issued}&`
  return {
    algorithm: 'SHA-256',
    challenge: createHash('sha256').update(`${salt}3`).digest('hex'),
    maxnumber: 10,
    salt,
    signature: 'f'.repeat(64),
  }
}

describe('createChallengeSolver and the worker', () => {
  it('dispose terminates the running worker and rejects what waits for it', async () => {
    const get = vi.fn(() => Promise.resolve(challengeFor(1000, 600)))
    const solver = createChallengeSolver({ get } as unknown as ApiClient, {
      sleep: () => Promise.resolve(),
    })
    solver.prepare()
    await vi.waitFor(() => expect(StubWorker.instances).toHaveLength(1))
    const waiting = solver.solved()
    const settled = expect(waiting).rejects.toBeInstanceOf(ChallengeError)

    solver.dispose()

    await settled
    expect(StubWorker.instances[0].terminated).toBe(1)
  })

  it('does nothing after dispose', async () => {
    const get = vi.fn(() => Promise.resolve(challengeFor(1000, 600)))
    const solver = createChallengeSolver({ get } as unknown as ApiClient)
    solver.dispose()

    solver.prepare()

    await expect(solver.solved()).rejects.toMatchObject({ kind: 'aborted' })
    expect(get).not.toHaveBeenCalled()
    expect(StubWorker.instances).toHaveLength(0)
  })

  it('fails a worker that never answers with its own error, not a hang', async () => {
    vi.useFakeTimers()
    const get = vi.fn(() => Promise.resolve(challengeFor(1000, 600)))
    const solver = createChallengeSolver({ get } as unknown as ApiClient, { solveTimeoutMs: 1000 })
    const settled = expect(solver.solved()).rejects.toMatchObject({ kind: 'timeout' })

    await vi.advanceTimersByTimeAsync(5000)

    await settled
    expect(StubWorker.instances.every((w) => w.terminated === 1)).toBe(true)
  })
})

describe('the expiry comes from the server timestamps', () => {
  // The visitor's clock says 1 000 000 ms. The server's timestamps are in 2026.
  // The lifetime is the difference of the two, whatever either clock reads.
  function setup(lifetime: number) {
    let clock = 1_000_000
    const get = vi.fn(() => Promise.resolve(challengeFor(1_790_000_000, lifetime)))
    const solve: SolveFunction = () => Promise.resolve(3)
    const solver = createChallengeSolver({ get } as unknown as ApiClient, {
      solve,
      now: () => clock,
      sleep: () => Promise.resolve(),
    })
    return { solver, get, advance: (ms: number) => (clock += ms) }
  }

  it('keeps a challenge that is longer than the headroom from its expiry', async () => {
    const { solver, get, advance } = setup(120)
    solver.prepare()
    await vi.waitFor(() => expect(get).toHaveBeenCalledTimes(1))
    await new Promise((resolve) => setTimeout(resolve, 5))
    advance(30_000)

    await solver.solved()

    expect(get).toHaveBeenCalledTimes(1)
  })

  it('refetches a challenge whose server lifetime leaves less than the headroom', async () => {
    const { solver, get, advance } = setup(120)
    solver.prepare()
    await vi.waitFor(() => expect(get).toHaveBeenCalledTimes(1))
    await new Promise((resolve) => setTimeout(resolve, 5))
    advance(61_000)

    await solver.solved()

    expect(get).toHaveBeenCalledTimes(2)
  })
})
