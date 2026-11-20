// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import type { ApiClient } from '../api/client'
import type { PowWorkerRequest, PowWorkerResponse } from './pow.worker'

/**
 * What the challenge route returns, the JSON of pow.Challenge in the Go
 * module. The field names are the ALTCHA ones: maxnumber is all lower case.
 */
export interface PowChallenge {
  algorithm: string
  challenge: string
  maxnumber: number
  salt: string
  signature: string
}

/** What the browser sends back, the JSON of pow.Solution in the Go module. */
export interface PowSolution {
  algorithm: string
  challenge: string
  number: number
  salt: string
  signature: string
}

/** Finds the number of a challenge, or null when none solves it. */
export type SolveFunction = (
  challenge: PowChallenge,
  signal?: AbortSignal,
) => Promise<number | null>

/** Why the solver failed on the visitor's side, as opposed to a refusal of the server. */
export type ChallengeErrorKind =
  /** The challenge has a salt the Go parser would refuse. */
  | 'salt'
  /** The worker could not start or crashed. */
  | 'worker'
  /** The worker did not answer in time. */
  | 'timeout'
  /** The search ended without a number. */
  | 'no_number'
  /** The solver was disposed while it worked. */
  | 'aborted'

/** ChallengeError is a failure of the browser's own proof of work, not of the network. */
export class ChallengeError extends Error {
  readonly kind: ChallengeErrorKind

  constructor(kind: ChallengeErrorKind, message: string) {
    super(message)
    this.name = 'ChallengeError'
    this.kind = kind
  }
}

export interface ChallengeSolverOptions {
  /** The challenge route, relative to the client's base URL. Default '/contact/challenge'. */
  path?: string
  /**
   * How long to wait after a challenge arrived before a solution may go out,
   * in milliseconds. The server refuses a solution younger than its minimum
   * age (pow.DefaultMinAge, 3 s), so this must be at least that plus a margin
   * for the round trip. A server with a longer MinAge needs a larger value.
   * Default 3500.
   */
  minAgeMs?: number
  /**
   * A solution this close to its expiry is not sent: a slow connection could
   * land it past the server's deadline, so a fresh challenge is prepared.
   * Default 60000.
   */
  headroomMs?: number
  /** How long the worker may take before the attempt fails. Default 60000. */
  solveTimeoutMs?: number
  /** The search. Default: a same origin Web Worker. A test passes its own. */
  solve?: SolveFunction
  /** The clock in milliseconds. Default Date.now. */
  now?: () => number
  /** The wait. Default setTimeout. */
  sleep?: (ms: number) => Promise<void>
}

export interface ChallengeSolver {
  /**
   * Starts fetching and solving a challenge, without waiting for it. Call it
   * on the first focus of the form. It does nothing while a usable challenge
   * is pending, and it replaces one that is about to expire.
   */
  prepare: () => void
  /**
   * Resolves to a solution that is old enough and not about to expire,
   * fetching and solving a fresh challenge first when needed. Every solution
   * is good for one submission: the next call prepares another.
   */
  solved: () => Promise<PowSolution>
  /** Stops the worker and drops every pending attempt. The solver is not usable after it. */
  dispose: () => void
}

interface Attempt {
  promise: Promise<Ready>
  /** Set when the challenge arrived; zero before. */
  expiresAt: number
}

interface Ready {
  solution: PowSolution
  receivedAt: number
  expiresAt: number
}

/**
 * parseSalt reads the one salt shape of the Go pow package:
 * `<24 hex>?expires=<unix>&issued=<unix>&`. The trailing ampersand is
 * required: it ends issued, so no digit of the salt can move into the
 * number (CVE-2025-68113). A salt without it is refused, as in the Go
 * parser and in pow/testdata/vectors.json.
 */
export function parseSalt(salt: string): { expires: number; issued: number } {
  const match = /^[0-9a-f]{24}\?expires=(0|[1-9]\d*)&issued=(0|[1-9]\d*)&$/.exec(salt)
  if (!match) {
    throw new ChallengeError('salt', 'govue pow: the salt has an unknown shape')
  }
  return { expires: Number(match[1]), issued: Number(match[2]) }
}

/**
 * solveInWorker runs the search in a same origin module Worker, which has no
 * network and no storage access of its own to use. The literal form of the
 * `new Worker(new URL(...))` call is what a bundler follows. The worker is
 * terminated on an answer, on an error, on the timeout and on the signal.
 */
export function solveInWorker(
  challenge: PowChallenge,
  signal?: AbortSignal,
  timeoutMs = 60_000,
): Promise<number | null> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(new ChallengeError('aborted', 'govue pow: aborted'))
      return
    }
    let worker: Worker
    try {
      worker = new Worker(new URL('./pow.worker.ts', import.meta.url), { type: 'module' })
    } catch {
      reject(new ChallengeError('worker', 'govue pow: the worker could not start'))
      return
    }
    const state: { timer?: ReturnType<typeof setTimeout> } = {}
    const finish = (settle: () => void) => {
      clearTimeout(state.timer)
      signal?.removeEventListener('abort', onAbort)
      worker.terminate()
      settle()
    }
    const onAbort = () => finish(() => reject(new ChallengeError('aborted', 'govue pow: aborted')))
    signal?.addEventListener('abort', onAbort, { once: true })
    state.timer = setTimeout(
      () => finish(() => reject(new ChallengeError('timeout', 'govue pow: the worker timed out'))),
      timeoutMs,
    )
    worker.onmessage = (event: MessageEvent<PowWorkerResponse>) =>
      finish(() => resolve(event.data.number))
    const failed = () =>
      finish(() => reject(new ChallengeError('worker', 'govue pow: the worker failed')))
    worker.onerror = failed
    worker.onmessageerror = failed
    worker.postMessage({
      challenge: challenge.challenge,
      salt: challenge.salt,
      maxnumber: challenge.maxnumber,
    } satisfies PowWorkerRequest)
  })
}

/**
 * createChallengeSolver fetches the proof of work challenge on the first
 * focus, solves it off the main thread, waits out the minimum age and
 * refetches when the solution is close to its expiry. It sets no cookie,
 * sends no header of its own and calls only the client it is given.
 */
export function createChallengeSolver(
  client: ApiClient,
  options: ChallengeSolverOptions = {},
): ChallengeSolver {
  const path = options.path ?? '/contact/challenge'
  const minAgeMs = options.minAgeMs ?? 3500
  const headroomMs = options.headroomMs ?? 60_000
  const solveTimeoutMs = options.solveTimeoutMs ?? 60_000
  const solve: SolveFunction =
    options.solve ?? ((challenge, signal) => solveInWorker(challenge, signal, solveTimeoutMs))
  const now = options.now ?? Date.now
  const sleep = options.sleep ?? ((ms: number) => new Promise<void>((r) => setTimeout(r, ms)))
  const controller = new AbortController()

  let current: Attempt | null = null

  async function run(attempt: Attempt): Promise<Ready> {
    const challenge = await client.get<PowChallenge>(path, {
      cache: 'no-store',
      signal: controller.signal,
    })
    const { expires, issued } = parseSalt(challenge.salt)
    const receivedAt = now()
    // The lifetime is the difference of two server timestamps, applied to the
    // visitor's clock from the moment of arrival. A visitor clock that is
    // hours off cannot make a challenge look valid or expired.
    const expiresAt = receivedAt + (expires - issued) * 1000
    attempt.expiresAt = expiresAt
    const number = await solve(challenge, controller.signal)
    if (number === null || !Number.isInteger(number)) {
      throw new ChallengeError('no_number', 'govue pow: no number solves the challenge')
    }
    return {
      receivedAt,
      expiresAt,
      solution: {
        algorithm: challenge.algorithm,
        challenge: challenge.challenge,
        number,
        salt: challenge.salt,
        signature: challenge.signature,
      },
    }
  }

  function start(): Attempt {
    const attempt: Attempt = { promise: undefined as unknown as Promise<Ready>, expiresAt: 0 }
    attempt.promise = controller.signal.aborted
      ? Promise.reject(new ChallengeError('aborted', 'govue pow: aborted'))
      : run(attempt)
    // solved() surfaces the failure. This only keeps a prepare that nobody
    // awaits from ending as an unhandled rejection.
    attempt.promise.catch(() => undefined)
    current = attempt
    return attempt
  }

  function usable(attempt: Attempt | null): attempt is Attempt {
    return attempt !== null && (attempt.expiresAt === 0 || attempt.expiresAt - now() >= headroomMs)
  }

  return {
    prepare: () => {
      if (!usable(current)) {
        start()
      }
    },
    solved: async () => {
      let attempt = usable(current) ? current : start()
      let ready: Ready
      try {
        ready = await attempt.promise
      } catch (caught) {
        if (caught instanceof ChallengeError && caught.kind === 'aborted') {
          throw caught
        }
        // A failed attempt is retried once, so one lost request does not fail
        // a submission. A second failure reaches the caller.
        attempt = start()
        ready = await attempt.promise
      }
      if (ready.expiresAt - now() < headroomMs) {
        attempt = start()
        ready = await attempt.promise
      }
      const wait = ready.receivedAt + minAgeMs - now()
      if (wait > 0) {
        await sleep(wait)
      }
      // One use: the server records a solved challenge and refuses a repeat.
      if (current === attempt) {
        current = null
      }
      return ready.solution
    },
    dispose: () => {
      current = null
      controller.abort()
    },
  }
}
